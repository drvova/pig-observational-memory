package gitrepo

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/drvova/pig-observational-memory/memory/host"
)

// The HEAD structure view (docs/brain-memory.md, decision 20): the adapter answers the engine's
// host.StructureReader from HEAD's tree, read once per (repository, HEAD) and kept in memory
// with the symbol searches and diffs it answered, so a commit that does not move costs one tree
// read for the life of the process. Nothing of it is written anywhere.

// The bounds of the view.
var (
	// MaxTreeFiles bounds the file list read from HEAD; past it the view is truncated and a
	// path it does not hold is unknown, never gone.
	MaxTreeFiles = 100_000
	// MaxListLimit bounds one page of the file list; DefaultListLimit is a page with no limit.
	MaxListLimit     = 500
	DefaultListLimit = 50
	// MaxQueryItems bounds the paths, symbols and diffs one query asks about.
	MaxQueryItems = 400
	// MaxDiffLines and MaxDiffBytes bound one diff.
	MaxDiffLines = 40
	MaxDiffBytes = 2400
	// MaxSymbolBytes bounds a searched symbol.
	MaxSymbolBytes = 200
	// StructureTimeout bounds the symbol searches, blob reads and diffs of one query together:
	// past it the rest are answered unknown, so a compaction never waits long on the view.
	StructureTimeout = 10 * time.Second
	// maxViews is how many (repository, HEAD) views the process keeps.
	maxViews = 4
)

// view is one HEAD's tree and the answers given at it.
type view struct {
	mu        sync.Mutex
	files     []string
	blobs     map[string]string
	truncated bool
	symbols   map[host.SymbolQuery]host.SymbolAnswer
	diffs     map[host.DiffQuery]host.DiffAnswer
	atBlobs   map[host.DiffQuery]host.BlobAnswer
}

var (
	viewsMu   sync.Mutex
	views     = map[string]*view{}
	viewOrder []string
	// treeReads counts the tree reads, for tests.
	treeReads int
)

// viewKey is a view's cache key: the repository's id (its common directory before the id is
// known) and HEAD.
func viewKey(repo host.Repo) string {
	id := repo.ID
	if id == "" {
		id = "dir:" + repo.CommonDir
	}
	return id + "\x00" + repo.Head
}

// viewOf is repo's view at its HEAD, read on first use.
func viewOf(dir string, repo host.Repo) (*view, error) {
	key := viewKey(repo)
	viewsMu.Lock()
	if v, ok := views[key]; ok {
		viewsMu.Unlock()
		return v, nil
	}
	viewsMu.Unlock()
	v, err := readTree(dir, repo.Head)
	if err != nil {
		return nil, err
	}
	viewsMu.Lock()
	defer viewsMu.Unlock()
	if old, ok := views[key]; ok {
		return old, nil
	}
	treeReads++
	views[key] = v
	viewOrder = append(viewOrder, key)
	for len(viewOrder) > maxViews {
		delete(views, viewOrder[0])
		viewOrder = viewOrder[1:]
	}
	return v, nil
}

// readTree lists the tree of commit head (git ls-tree -r), at most MaxTreeFiles entries.
func readTree(dir, head string) (*view, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "ls-tree", "-r", "-z", "--full-tree", head)
	cmd.Env = gitEnv()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	v := &view{blobs: map[string]string{}, symbols: map[host.SymbolQuery]host.SymbolAnswer{}, diffs: map[host.DiffQuery]host.DiffAnswer{}, atBlobs: map[host.DiffQuery]host.BlobAnswer{}}
	r := bufio.NewReaderSize(out, 64*1024)
	for {
		rec, err := r.ReadString(0)
		if err != nil {
			break
		}
		if len(v.files) >= MaxTreeFiles {
			v.truncated = true
			break
		}
		rec = strings.TrimSuffix(rec, "\x00")
		meta, p, ok := strings.Cut(rec, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			continue
		}
		v.files = append(v.files, p)
		v.blobs[p] = fields[2]
	}
	if v.truncated {
		// Stop the listing: the rest is not read.
		cancel()
		_ = cmd.Wait()
	} else if err := cmd.Wait(); err != nil {
		return nil, err
	}
	if ctx.Err() != nil && !v.truncated {
		return nil, errors.New("gitrepo: tree read timed out")
	}
	sort.Strings(v.files)
	return v, nil
}

// Structure answers q at repo's HEAD, read by Read from dir. ok is false outside a repository
// or before the first commit.
func Structure(dir string, repo host.Repo, q host.StructureQuery) (host.StructureAnswer, bool, error) {
	if repo.Head == "" || repo.Root == "" {
		return host.StructureAnswer{}, false, nil
	}
	v, err := viewOf(dir, repo)
	if err != nil {
		return host.StructureAnswer{}, false, err
	}
	a := host.StructureAnswer{RepoID: repo.ID, Head: repo.Head, Truncated: v.truncated}
	deadline := time.Now().Add(StructureTimeout)
	for i, p := range q.Paths {
		if i >= MaxQueryItems {
			break
		}
		a.Paths = append(a.Paths, v.path(repo, p))
	}
	for i, s := range q.Symbols {
		if i >= MaxQueryItems {
			break
		}
		a.Symbols = append(a.Symbols, v.symbol(dir, repo, s, deadline))
	}
	for i, b := range q.Blobs {
		if i >= MaxQueryItems {
			break
		}
		a.Blobs = append(a.Blobs, v.blob(dir, repo, b, deadline))
	}
	for i, d := range q.Diffs {
		if i >= MaxQueryItems {
			break
		}
		a.Diffs = append(a.Diffs, v.diff(dir, repo, d, deadline))
	}
	if q.List != nil {
		a.Files, a.Total = v.list(*q.List)
	}
	return a, true, nil
}

// Rel is p as a path of HEAD's tree: slash-separated and cleaned, made relative to the work
// tree's root when absolute; ok is false for a path outside the work tree.
func Rel(root, p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", false
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return "", false
		}
		p = rel
	}
	p = path.Clean(filepath.ToSlash(p))
	if p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") {
		return "", false
	}
	return p, true
}

func (v *view) path(repo host.Repo, p string) host.PathAnswer {
	rel, ok := Rel(repo.Root, p)
	if !ok {
		return host.PathAnswer{Path: p}
	}
	if blob, ok := v.blobs[rel]; ok {
		return host.PathAnswer{Path: p, Known: true, Exists: true, Blob: blob}
	}
	if v.truncated {
		return host.PathAnswer{Path: p}
	}
	if v.hasDir(rel) {
		// A directory of the tree exists; it has no blob of its own.
		return host.PathAnswer{Path: p, Known: true, Exists: true}
	}
	return host.PathAnswer{Path: p, Known: true}
}

// hasDir reports whether rel is a directory of the tree.
func (v *view) hasDir(rel string) bool {
	prefix := rel + "/"
	i := sort.SearchStrings(v.files, prefix)
	return i < len(v.files) && strings.HasPrefix(v.files[i], prefix)
}

func (v *view) symbol(dir string, repo host.Repo, q host.SymbolQuery, deadline time.Time) host.SymbolAnswer {
	out := host.SymbolAnswer{Path: q.Path, Symbol: q.Symbol}
	sym := strings.TrimSpace(q.Symbol)
	if sym == "" || len(sym) > MaxSymbolBytes || strings.ContainsAny(sym, "\n\r\x00") {
		return out
	}
	rel := ""
	if strings.TrimSpace(q.Path) != "" {
		var ok bool
		if rel, ok = Rel(repo.Root, q.Path); !ok {
			return out
		}
		if p := v.path(repo, rel); !p.Known {
			return out
		} else if !p.Exists {
			out.Known = true
			return out
		}
	}
	key := host.SymbolQuery{Path: rel, Symbol: sym}
	v.mu.Lock()
	if cached, ok := v.symbols[key]; ok {
		v.mu.Unlock()
		cached.Path, cached.Symbol = q.Path, q.Symbol
		return cached
	}
	v.mu.Unlock()
	if time.Now().After(deadline) {
		return out
	}
	args := []string{"grep", "-q", "-I", "-w", "-F", "-e", sym, repo.Head}
	if rel != "" {
		args = append(args, "--", rel)
	}
	_, code, err := gitRaw(Timeout, dir, 0, args...)
	switch {
	case err == nil && code == 0:
		out.Known, out.Found = true, true
	case err == nil && code == 1:
		out.Known = true
	default:
		return out
	}
	v.mu.Lock()
	v.symbols[key] = out
	v.mu.Unlock()
	return out
}

// commitHash is a commit a diff may start from: hex only, so it can never read as an option.
var commitHash = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

func (v *view) blob(dir string, repo host.Repo, q host.BlobQuery, deadline time.Time) host.BlobAnswer {
	out := host.BlobAnswer{Path: q.Path, At: q.At}
	rel, ok := Rel(repo.Root, q.Path)
	if !ok || !commitHash.MatchString(q.At) {
		return out
	}
	if q.At == repo.Head {
		if blob, ok := v.blobs[rel]; ok {
			out.Known, out.Blob = true, blob
		}
		return out
	}
	key := host.DiffQuery{Path: rel, From: q.At}
	v.mu.Lock()
	if cached, ok := v.atBlobs[key]; ok {
		v.mu.Unlock()
		cached.Path, cached.At = q.Path, q.At
		return cached
	}
	v.mu.Unlock()
	if time.Now().After(deadline) {
		return out
	}
	raw, code, err := gitRaw(Timeout, dir, 256, "rev-parse", "--verify", "--quiet", q.At+":"+rel)
	if err != nil {
		return out
	}
	if blob := strings.TrimSpace(string(raw)); code == 0 && blob != "" {
		out.Known, out.Blob = true, blob
	}
	v.mu.Lock()
	v.atBlobs[key] = out
	v.mu.Unlock()
	return out
}

func (v *view) diff(dir string, repo host.Repo, q host.DiffQuery, deadline time.Time) host.DiffAnswer {
	out := host.DiffAnswer{Path: q.Path, From: q.From}
	rel, ok := Rel(repo.Root, q.Path)
	if !ok || !commitHash.MatchString(q.From) {
		return out
	}
	key := host.DiffQuery{Path: rel, From: q.From}
	v.mu.Lock()
	if cached, ok := v.diffs[key]; ok {
		v.mu.Unlock()
		cached.Path, cached.From = q.Path, q.From
		return cached
	}
	v.mu.Unlock()
	if time.Now().After(deadline) {
		return out
	}
	raw, code, err := gitRaw(Timeout, dir, 64*1024, "diff", "--no-color", "--no-ext-diff", "-U2", q.From, repo.Head, "--", rel)
	if err != nil || code != 0 {
		return out
	}
	out.Known = true
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	var kept []string
	size := 0
	for _, line := range lines {
		if line == "" && len(lines) == 1 {
			break
		}
		if len(kept) >= MaxDiffLines || size+len(line)+1 > MaxDiffBytes {
			out.Truncated = true
			break
		}
		kept = append(kept, line)
		size += len(line) + 1
	}
	out.Diff = strings.Join(kept, "\n")
	v.mu.Lock()
	v.diffs[key] = out
	v.mu.Unlock()
	return out
}

func (v *view) list(q host.ListQuery) ([]string, int) {
	prefix := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(q.Prefix)), "./")
	lo := sort.SearchStrings(v.files, prefix)
	hi := lo
	for hi < len(v.files) && strings.HasPrefix(v.files[hi], prefix) {
		hi++
	}
	total := hi - lo
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	limit = min(limit, MaxListLimit)
	offset := max(q.Offset, 0)
	if offset >= total {
		return nil, total
	}
	end := min(lo+offset+limit, hi)
	return append([]string(nil), v.files[lo+offset:end]...), total
}

// gitEnv is the environment every git command runs with: no optional locks, the C locale, no
// prompt.
func gitEnv() []string {
	return append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
}

// gitRaw runs one git command in dir and returns its output (at most limit bytes, 0 for none)
// and exit code; err is a failure to run it or a timeout.
func gitRaw(timeout time.Duration, dir string, limit int, args ...string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	var buf limitedBuffer
	buf.limit = limit
	cmd.Stdout = &buf
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, -1, errors.New("gitrepo: timed out")
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return buf.Bytes(), exit.ExitCode(), nil
	}
	if err != nil {
		return nil, -1, err
	}
	return buf.Bytes(), 0, nil
}

// limitedBuffer keeps the first limit bytes written and drops the rest.
type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}
