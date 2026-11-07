package runtime_test

import (
	"errors"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/runtime"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// The host's repository is read once per key (the working directory and the latest
// compaction), so git runs once between two compactions; a failed read is not kept.
func TestCachedRepoReadsOncePerKey(t *testing.T) {
	rt := runtime.New()
	h := testfix.NewFakeHost()
	h.RepoErr = errors.New("git failed")
	if _, ok := rt.CachedRepo("k1", h); ok || h.RepoCalls != 1 {
		t.Fatalf("a failed read: ok %v, %d calls", ok, h.RepoCalls)
	}
	h.RepoErr = nil
	h.RepoInfo = &host.Repo{ID: "0123456789ab", Head: "h1"}
	for range 3 {
		if repo, ok := rt.CachedRepo("k1", h); !ok || repo.Head != "h1" {
			t.Fatalf("repo %+v ok %v", repo, ok)
		}
	}
	if h.RepoCalls != 2 {
		t.Fatalf("%d reads for one key, want 2 (the failed one and one more)", h.RepoCalls)
	}
	h.RepoInfo = &host.Repo{ID: "0123456789ab", Head: "h2"}
	if repo, _ := rt.CachedRepo("k1", h); repo.Head != "h1" {
		t.Fatalf("the cached repository changed within a key: %+v", repo)
	}
	if repo, _ := rt.CachedRepo("k2", h); repo.Head != "h2" || h.RepoCalls != 3 {
		t.Fatalf("a new compaction did not read again: %+v, %d calls", repo, h.RepoCalls)
	}
}
