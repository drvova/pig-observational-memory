//go:build !unix && !windows

package filelock

import "sync"

// lockMu serializes the writers in this process. These platforms have no file lock the
// standard library reaches, so two processes writing one file there are not serialized: the
// rename keeps the file whole, and the last writer wins whole.
var lockMu sync.Mutex

func WithLock(_ string, fn func() error) error {
	lockMu.Lock()
	defer lockMu.Unlock()
	return fn()
}
