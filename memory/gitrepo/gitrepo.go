// Package gitrepo reads the git repository a session runs in, for an adapter to answer the
// engine's host.RepoReader (docs/brain-memory.md, "Project identity"): the canonical work-tree
// root, the common git directory, the origin remote and the root commit, the branch and HEAD.
// It runs git; the engine never imports it, so level 0 stays pure.
package gitrepo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/ids"
)

// Timeout bounds every git command: a root-commit walk over a very long history gives up rather
// than hold an event.
var Timeout = 5 * time.Second

// Read reads the repository holding dir with no id cache: the root-commit lookup runs every
// time, bounded by Timeout. ok is false when dir is in none, or git is missing.
func Read(dir string) (host.Repo, bool, error) { return Reader{}.Read(dir) }

// Reader reads repositories for an adapter. With a Store (a directory of the engine's own,
// never inside the repository or the harness's files), each repository's id is computed once,
// keyed by its common git directory, and cached in Store/repos (docs/brain-memory.md, scenario
// 37): a slow root-commit lookup never holds an event and never yields another id later.
type Reader struct {
	Store string
}

// Read reads the repository holding dir. ok is false when dir is in none, or git is missing.
// With a Store and no cached id, the root-commit lookup runs in the background and is waited on
// for RootWait at most: when it has not finished, the repository reads with no id (no record
// takes a frame from it) until the lookup lands in the cache.
func (r Reader) Read(dir string) (host.Repo, bool, error) {
	if strings.TrimSpace(dir) == "" {
		return host.Repo{}, false, nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return host.Repo{}, false, nil
	}
	lines, err := git(dir, "rev-parse", "--show-toplevel", "--path-format=absolute", "--git-common-dir")
	if err != nil || len(lines) < 2 {
		// Not a repository (or a bare one): no frame.
		return host.Repo{}, false, nil
	}
	repo := host.Repo{Root: canonical(lines[0]), CommonDir: canonical(lines[1])}
	if head, err := git(dir, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil && len(head) > 0 {
		repo.Head = head[0]
	}
	if branch, err := git(dir, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil && len(branch) > 0 {
		repo.Branch = branch[0]
	}
	if urls, err := git(dir, "config", "--get-regexp", `^remote\..*\.url$`); err == nil {
		repo.Remote = pickRemote(urls)
	}
	switch {
	case repo.Head == "":
		// Before the first commit the id is the common directory's, never cached: the first
		// commit gives the repository its lasting id.
		repo.ID = ID("", "", repo.CommonDir)
	case r.Store == "":
		if root, err := rootLookup(dir, Timeout); err == nil {
			repo.RootCommit = root
		}
		repo.ID = ID(repo.Remote, repo.RootCommit, repo.CommonDir)
	default:
		if cached, ok := loadID(r.Store, repo.CommonDir); ok {
			repo.ID, repo.RootCommit = cached.ID, cached.RootCommit
		} else if cached, ok := r.lookupRoot(dir, repo); ok {
			repo.ID, repo.RootCommit = cached.ID, cached.RootCommit
		}
	}
	return repo, true, nil
}

// rootLookup finds the oldest root commit of HEAD in dir; tests replace it with a slow one.
var rootLookup = func(dir string, timeout time.Duration) (string, error) {
	roots, err := gitWithin(timeout, dir, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return "", err
	}
	if len(roots) == 0 {
		return "", errors.New("gitrepo: no root commit")
	}
	// rev-list lists newest first: the last root is the oldest.
	return roots[len(roots)-1], nil
}

// ID is a repository's identity: the hash of its normalized remote and root commit, which
// survives re-clones, moves and worktrees; of the root commit alone without a remote; of the
// common git directory before the first commit. An alias table that joins a renamed remote's
// ids belongs to the core (docs/brain-memory.md, phase 6).
func ID(remote, rootCommit, commonDir string) string {
	switch {
	case rootCommit != "":
		return ids.HashID("repo\x00" + remote + "\x00" + rootCommit)
	case commonDir != "":
		return ids.HashID("repo-dir\x00" + commonDir)
	}
	return ""
}

// pickRemote is the origin remote's normalized URL, else the first remote's by name.
func pickRemote(lines []string) string {
	urls := map[string]string{}
	names := []string{}
	for _, line := range lines {
		key, url, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		if _, seen := urls[name]; !seen {
			names = append(names, name)
			urls[name] = url
		}
	}
	if url, ok := urls["origin"]; ok {
		return NormalizeRemote(url)
	}
	sort.Strings(names)
	if len(names) > 0 {
		return NormalizeRemote(urls[names[0]])
	}
	return ""
}

// NormalizeRemote is a remote URL as an identity: no scheme, no user or password, the host in
// lower case, no trailing ".git" or "/", and an scp-like "user@host:path" as "host/path"; a local
// path is cleaned.
func NormalizeRemote(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return ""
	}
	hostAndPath := ""
	if _, rest, ok := strings.Cut(url, "://"); ok {
		hostAndPath = rest
	} else if colon := strings.IndexByte(url, ':'); colon > 0 && !strings.ContainsRune(url[:colon], '/') {
		hostAndPath = url[:colon] + "/" + url[colon+1:]
	} else {
		return filepath.Clean(url)
	}
	authority, path, _ := strings.Cut(hostAndPath, "/")
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		authority = authority[at+1:]
	}
	path = strings.TrimSuffix(strings.TrimRight(path, "/"), ".git")
	path = strings.TrimRight(path, "/")
	out := strings.ToLower(authority)
	if path != "" {
		out += "/" + strings.TrimLeft(path, "/")
	}
	return out
}

func canonical(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return filepath.Clean(p)
}

// git runs one git command in dir and returns its non-empty output lines.
func git(dir string, args ...string) ([]string, error) { return gitWithin(Timeout, dir, args...) }

// gitWithin is git bounded by timeout.
func gitWithin(timeout time.Duration, dir string, args ...string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, errors.New("gitrepo: timed out")
	}
	lines := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}
