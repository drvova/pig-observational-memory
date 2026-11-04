//go:build !unix && !windows

package codestore

import "sync"

// lockMu serializes the store's writers in this process. These platforms have no file lock the
// standard library reaches, so two processes writing one store there are not serialized: the
// rename keeps the file whole, and the last writer wins whole.
var lockMu sync.Mutex

func withLock(_ string, fn func() error) error {
	lockMu.Lock()
	defer lockMu.Unlock()
	return fn()
}
