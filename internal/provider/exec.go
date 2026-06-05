package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// runCommand executes an external command with a timeout, capturing stdout and stderr.
// env is appended to the current process environment (each element in "KEY=VALUE" form).
func runCommand(ctx context.Context, name string, args []string, env []string, timeout time.Duration) (stdout, stderr string, err error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return stdout, stderr, fmt.Errorf("command %q timed out after %s: %w\nstderr: %s", name, timeout, err, stderr)
		}
		return stdout, stderr, fmt.Errorf("command %q failed: %w\nstderr: %s", name, err, stderr)
	}

	return stdout, stderr, nil
}

// readFileBytes reads a file and returns its contents.
func readFileBytes(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file %q: %w", path, err)
	}
	return data, nil
}

// findBinary returns the given binary path if it is an absolute or relative path
// with a path separator, otherwise resolves it from PATH.
func findBinary(binary string) (string, error) {
	if strings.ContainsRune(binary, '/') {
		return binary, nil
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return "", fmt.Errorf("binary %q not found on PATH: %w", binary, err)
	}
	return path, nil
}
