package gitrepo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/drvova/pig-observational-memory/memory/host"
)

// The repository id cache (docs/brain-memory.md, scenario 37; contracts/encoding.md, "Frame and
// project identity"): one file per repository under the engine's own store, keyed by the
// canonical common git directory, holding the id computed once from the root commit. A read
// with the file never runs the root-commit lookup, and the id it gives never changes. A missing
// or unreadable file (a store from before the cache, a crash mid-write) is computed again and
// written; nothing reads the repository's own files or the harness's.

// RootWait is how long a read with a store waits for a root-commit lookup it started; the
// lookup itself runs on for RootTimeout in the background and caches its id.
var (
	RootWait    = 2 * time.Second
	RootTimeout = 2 * time.Minute
)

// CachedID is one repository's cache file.
type CachedID struct {
	CommonDir  string `json:"commonDir"`
	ID         string `json:"repoId"`
	Remote     string `json:"remote"`
	RootCommit string `json:"rootCommit"`
	Computed   string `json:"computedAt"`
}

// IDPath is the cache file of the repository whose common git directory is commonDir.
func IDPath(store, commonDir string) string {
	sum := sha256.Sum256([]byte(commonDir))
	return filepath.Join(store, "repos", hex.EncodeToString(sum[:8])+".json")
}

// loadID reads commonDir's cached id; ok is false for none, or a file for another directory.
// The file is read first; an id this process computed but could not write (a read-only store)
// is kept in memory, so it is still never computed twice.
func loadID(store, commonDir string) (CachedID, bool) {
	var c CachedID
	if data, err := os.ReadFile(IDPath(store, commonDir)); err == nil &&
		json.Unmarshal(data, &c) == nil && c.CommonDir == commonDir && c.ID != "" && c.RootCommit != "" {
		return c, true
	}
	memoMu.Lock()
	defer memoMu.Unlock()
	c, ok := memo[store+"\x00"+commonDir]
	return c, ok
}

// memo holds the ids this process computed, by store and common directory.
var (
	memoMu sync.Mutex
	memo   = map[string]CachedID{}
)

// storeID writes c, through a temporary file renamed into place, unless an id is already held
// for the directory: the first id computed stands.
func storeID(store string, c CachedID) {
	if _, ok := loadID(store, c.CommonDir); ok {
		return
	}
	memoMu.Lock()
	memo[store+"\x00"+c.CommonDir] = c
	memoMu.Unlock()
	p := IDPath(store, c.CommonDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".repo-*.tmp")
	if err != nil {
		return
	}
	_, werr := tmp.Write(append(data, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
	}
}

// lookups are the root-commit lookups in flight, one per store and common directory.
var (
	lookupsMu sync.Mutex
	lookups   = map[string]chan struct{}{}
)

// lookupRoot starts (or joins) the background lookup of repo's root commit and waits RootWait
// for it. The lookup writes the id to the cache when it finishes, however long after.
func (r Reader) lookupRoot(dir string, repo host.Repo) (CachedID, bool) {
	key := r.Store + "\x00" + repo.CommonDir
	lookupsMu.Lock()
	done, running := lookups[key]
	if !running {
		done = make(chan struct{})
		lookups[key] = done
		go func() {
			defer func() {
				lookupsMu.Lock()
				delete(lookups, key)
				lookupsMu.Unlock()
				close(done)
			}()
			root, err := RootLookup(dir, RootTimeout)
			if err != nil || root == "" {
				return
			}
			storeID(r.Store, CachedID{CommonDir: repo.CommonDir, ID: ID(repo.Remote, root, repo.CommonDir),
				Remote: repo.Remote, RootCommit: root, Computed: time.Now().UTC().Format(time.RFC3339)})
		}()
	}
	lookupsMu.Unlock()
	select {
	case <-done:
	case <-time.After(RootWait):
		return CachedID{}, false
	}
	return loadID(r.Store, repo.CommonDir)
}
