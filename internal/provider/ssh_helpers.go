package provider

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

// sha256File returns the hex-encoded SHA256 checksum of a file.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// bytesReader wraps a byte slice as an io.Reader for SSH stdin.
func bytesReader(b []byte) io.Reader {
	return bytes.NewReader(b)
}
