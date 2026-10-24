//go:build !unix

package curated

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// staleLock is how old a lock file must be before another writer takes it
// over: longer than any store write, so only a crashed writer's lock is stale.
const staleLock = 30 * time.Second

// locked runs fn holding the store's lock file, created exclusively; where
// there is no flock, a writer waits for the file to go away.
func (s Store) locked(fn func() error) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(s.Dir, ".lock")
	deadline := time.Now().Add(10 * time.Second)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > staleLock {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return errors.New("the memory store is locked by another writer")
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer os.Remove(path)
	return fn()
}
