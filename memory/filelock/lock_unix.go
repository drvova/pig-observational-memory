//go:build unix

// Package filelock is the cross-process lock the memory stores take around each write.
package filelock

import (
	"os"
	"syscall"
)

// WithLock runs fn holding an exclusive flock on path, shared by every process (and every open
// of the file in this one) that writes the file it guards.
func WithLock(path string, fn func() error) error {
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

// TryWithLock is WithLock that does not wait: held is false, and fn does not run, when another
// open holds the lock.
func TryWithLock(path string, fn func() error) (held bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if err == syscall.EWOULDBLOCK {
			return false, nil
		}
		return false, err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return true, fn()
}
