//go:build windows

package filelock

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

// lockfileExclusiveLock is LOCKFILE_EXCLUSIVE_LOCK: wait for an exclusive lock.
const lockfileExclusiveLock = 0x2

// WithLock runs fn holding an exclusive LockFileEx lock on the first byte of path, shared by
// every process (and every handle in this one) that writes the file it guards. Untested: CI only vets
// the Windows build.
func WithLock(path string, fn func() error) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	var ol syscall.Overlapped
	if r, _, err := procLockFileEx.Call(f.Fd(), lockfileExclusiveLock, 0, 1, 0, uintptr(unsafe.Pointer(&ol))); r == 0 {
		return err
	}
	defer procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
	return fn()
}

// TryWithLock is WithLock on Windows, which waits for the lock (untested there, as WithLock).
func TryWithLock(path string, fn func() error) (bool, error) { return true, WithLock(path, fn) }
