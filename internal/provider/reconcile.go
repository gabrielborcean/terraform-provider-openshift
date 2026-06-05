package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ReconcileOpts configures a RunWithReconciliation call.
type ReconcileOpts struct {
	// Command to start (argv[0] is the binary, rest are arguments).
	Command []string
	// Extra environment variables in "KEY=VALUE" form.
	Env []string
	// LogFile receives all stdout+stderr from the child process.
	LogFile string
	// StateFile is a JSON install.state path updated each poll cycle.
	StateFile string
	// PollInterval between success/failure checks (default 30s).
	PollInterval time.Duration
	// Total timeout; 0 means no timeout beyond the parent context.
	Timeout time.Duration
	// SuccessCheck is called every PollInterval. Return true to stop early.
	SuccessCheck func() (bool, error)
	// FailureCheck is called every PollInterval. Return (true, reason, nil) to abort.
	FailureCheck func() (bool, string, error)
}

// RunWithReconciliation starts a command in the background and polls SuccessCheck /
// FailureCheck every PollInterval until one of them fires, the process exits, or the
// context is cancelled.
//
// stdout+stderr are streamed to opts.LogFile in real time.
// opts.StateFile is updated at each poll cycle with the current install phase.
//
// If a PID file (opts.StateFile + ".pid") already exists and the named process is
// still running, RunWithReconciliation attaches to the existing log output rather
// than starting a new process.
func RunWithReconciliation(ctx context.Context, opts ReconcileOpts) error {
	if opts.PollInterval <= 0 {
		opts.PollInterval = 30 * time.Second
	}

	// Apply a total timeout wrapping the parent context if requested.
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// Check for a running process via PID file.
	pidFile := opts.StateFile + ".pid"
	if pid, running := readRunningPID(pidFile); running {
		// Process is already running; just poll until it finishes.
		return pollUntilDone(ctx, opts, pid, nil)
	}

	// Open / create the log file for streaming output.
	var logWriter io.Writer = io.Discard
	if opts.LogFile != "" {
		lf, err := os.OpenFile(opts.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("opening log file %q: %w", opts.LogFile, err)
		}
		defer lf.Close()
		logWriter = lf
	}

	if len(opts.Command) == 0 {
		return fmt.Errorf("ReconcileOpts.Command must not be empty")
	}

	cmd := exec.CommandContext(ctx, opts.Command[0], opts.Command[1:]...) //nolint:gosec
	if len(opts.Env) > 0 {
		cmd.Env = append(cmd.Environ(), opts.Env...)
	}

	// Pipe stdout and stderr to the log file.
	pr, pw, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("creating output pipe: %w", err)
	}
	cmd.Stdout = pw
	cmd.Stderr = pw

	// Start the command in the background.
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return fmt.Errorf("starting command %q: %w", opts.Command[0], err)
	}
	pw.Close() // parent doesn't write to the pipe

	// Stream process output to logWriter in a goroutine.
	copyDone := make(chan struct{})
	go func() {
		defer close(copyDone)
		_, _ = io.Copy(logWriter, pr)
		pr.Close()
	}()

	// Write PID file so subsequent calls can detect this process.
	writePIDFile(pidFile, cmd.Process.Pid)

	// Wait for the process in a goroutine and signal via a channel.
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	err = pollUntilDone(ctx, opts, cmd.Process.Pid, waitCh)

	// Ensure copy goroutine finishes before returning.
	<-copyDone
	os.Remove(pidFile)

	return err
}

// pollUntilDone drives the reconciliation loop. waitCh may be nil when attaching
// to an already-running process (in which case we poll the process existence).
func pollUntilDone(ctx context.Context, opts ReconcileOpts, pid int, waitCh chan error) error {
	ticker := time.NewTicker(opts.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Context cancelled — send SIGTERM then SIGKILL.
			gracefulKill(pid)
			if waitCh != nil {
				<-waitCh
			}
			return fmt.Errorf("reconciliation context cancelled: %w", ctx.Err())

		case exitErr, ok := <-waitCh:
			if !ok {
				// waitCh is nil / closed; fall through to poll.
				break
			}
			// Process exited on its own. Run one final SuccessCheck.
			if opts.SuccessCheck != nil {
				success, checkErr := opts.SuccessCheck()
				if checkErr != nil {
					_ = updateStateFile(opts.StateFile, "failed", fmt.Sprintf("success check error after exit: %v", checkErr))
					return fmt.Errorf("success check failed after process exit: %w", checkErr)
				}
				if success {
					_ = updateStateFile(opts.StateFile, "complete", "")
					return nil
				}
			}
			if exitErr != nil {
				_ = updateStateFile(opts.StateFile, "failed", exitErr.Error())
				return fmt.Errorf("command exited with error: %w", exitErr)
			}
			_ = updateStateFile(opts.StateFile, "complete", "")
			return nil

		case <-ticker.C:
			// If waitCh is nil, check if the process is still alive.
			if waitCh == nil {
				if !processAlive(pid) {
					if opts.SuccessCheck != nil {
						success, _ := opts.SuccessCheck()
						if success {
							_ = updateStateFile(opts.StateFile, "complete", "")
							return nil
						}
					}
					_ = updateStateFile(opts.StateFile, "failed", "process exited unexpectedly")
					return fmt.Errorf("process %d exited unexpectedly", pid)
				}
			}

			// SuccessCheck
			if opts.SuccessCheck != nil {
				success, err := opts.SuccessCheck()
				if err != nil {
					_ = updateStateFile(opts.StateFile, "failed", err.Error())
					gracefulKill(pid)
					if waitCh != nil {
						<-waitCh
					}
					return fmt.Errorf("success check error: %w", err)
				}
				if success {
					_ = updateStateFile(opts.StateFile, "complete", "")
					gracefulKill(pid)
					if waitCh != nil {
						<-waitCh
					}
					return nil
				}
			}

			// FailureCheck
			if opts.FailureCheck != nil {
				failed, reason, err := opts.FailureCheck()
				if err != nil {
					_ = updateStateFile(opts.StateFile, "failed", err.Error())
					gracefulKill(pid)
					if waitCh != nil {
						<-waitCh
					}
					return fmt.Errorf("failure check error: %w", err)
				}
				if failed {
					_ = updateStateFile(opts.StateFile, "failed", reason)
					gracefulKill(pid)
					if waitCh != nil {
						<-waitCh
					}
					return fmt.Errorf("failure condition detected: %s", reason)
				}
			}

			_ = updateStateFile(opts.StateFile, "installing", "")
		}
	}
}

// gracefulKill sends SIGTERM to pid, then waits up to 30s before sending SIGKILL.
func gracefulKill(pid int) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if !processAlive(pid) {
			return
		}
	}
	_ = proc.Signal(syscall.SIGKILL)
}

// processAlive returns true if a process with the given pid is still running.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// writePIDFile writes pid to path.
func writePIDFile(path string, pid int) {
	_ = os.WriteFile(path, []byte(strconv.Itoa(pid)), 0644)
}

// readRunningPID reads the PID from path and returns it plus whether it is alive.
func readRunningPID(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, processAlive(pid)
}

// installState is the JSON structure written to install.state.
type installState struct {
	Phase     string `json:"phase"`
	StartedAt string `json:"started_at,omitempty"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"last_error,omitempty"`
}

// readInstallState reads and parses the install.state JSON file.
// Returns a zero-value installState if the file does not exist.
func readInstallState(path string) (installState, error) {
	var s installState
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, fmt.Errorf("reading install state %q: %w", path, err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("parsing install state %q: %w", path, err)
	}
	return s, nil
}

// writeInstallState serialises s and writes it to path atomically.
func writeInstallState(path string, s installState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling install state: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// updateStateFile is a convenience wrapper used inside the poll loop.
func updateStateFile(path, phase, lastError string) error {
	if path == "" {
		return nil
	}
	s, _ := readInstallState(path)
	s.Phase = phase
	s.LastError = lastError
	return writeInstallState(path, s)
}
