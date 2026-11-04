//go:build unix

package codestore

import (
	"os"
	"syscall"
)

// withLock runs fn holding an exclusive flock on path, shared by every process (and every open
// of the file in this one) that writes the store.
func withLock(path string, fn func() error) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
