// Package testutil provides helpers for acceptance tests against CRC clusters.
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// CRCKubeconfig returns the path to the CRC kubeconfig and true if found.
// It checks, in order:
//  1. The KUBECONFIG environment variable.
//  2. ~/.crc/machines/crc/kubeconfig (the default CRC location).
func CRCKubeconfig() (string, bool) {
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		if _, err := os.Stat(kc); err == nil {
			return kc, true
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	crcKubeconfig := filepath.Join(home, ".crc", "machines", "crc", "kubeconfig")
	if _, err := os.Stat(crcKubeconfig); err == nil {
		return crcKubeconfig, true
	}

	return "", false
}

// SkipIfNoCRC skips the test with a clear message when no CRC kubeconfig is available.
func SkipIfNoCRC(t *testing.T) {
	t.Helper()
	if _, ok := CRCKubeconfig(); !ok {
		t.Skip(
			"Skipping CRC acceptance test: no kubeconfig found. " +
				"Set KUBECONFIG or start CRC so ~/.crc/machines/crc/kubeconfig exists.",
		)
	}
}
