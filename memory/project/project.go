// Package project is memory v2's project knowledge (docs/brain-memory.md, decision 20;
// contracts/encoding.md, "Project records"), as pure code: which records are per-repository
// project records, the one symbol a record names, the questions a compaction asks the adapter's
// HEAD structure view (host.StructureReader) about them, and the freshness verdicts its answer
// gives. The structure itself is never stored: the engine asks, judges and writes nothing of it.
// lisp/src/project.lisp is the image's copy; contracts/project.jsonl holds the cases both check.
package project

import (
	"path"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// MaxRecords bounds the project records one compaction asks about, newest first.
const MaxRecords = 200

// MaxSymbols bounds the symbols one compaction searches for (a search with no path reads the
// whole tree), the newest kept.
const MaxSymbols = 20

// MaxDiffs bounds the diffs the level-1 input shows (the possibly stale list).
const MaxDiffs = 8

// ShortSHA is how many hex characters of a commit a verdict prints.
const ShortSHA = 7

// The verdict kinds.
const (
	Gone    = "gone"
	Changed = "changed"
)

// IsRecord reports a per-repository project record: a record framed in a repository
// (Frame.RepoID) that keeps the why, not the structure: a decision, a repository convention, or
// the user's own rule, correction or stated fact (a contract the code does not show, a pitfall).
func IsRecord(o ledger.Observation) bool {
	if o.Frame.RepoID == "" {
		return false
	}
	switch o.Kind {
	case ledger.KindDecision, ledger.KindConvention:
		return true
	case "preference", "correction", "fact":
		return o.Source == ledger.SourceUserSaid
	}
	return false
}

// SymbolIn is the one code symbol text names in backticks, "" for none or more than one: an
// identifier of ASCII letters, digits and _ (optionally followed by "()") that reads as code, with
// an underscore, a lower-case letter followed by an upper-case one, or the "()". A path, a dotted
// name or a plain word is none.
func SymbolIn(text string) string {
	found := ""
	rest := text
	for {
		open := strings.IndexByte(rest, '`')
		if open < 0 {
			return found
		}
		end := strings.IndexByte(rest[open+1:], '`')
		if end < 0 {
			return found
		}
		token := rest[open+1 : open+1+end]
		rest = rest[open+1+end+1:]
		name, ok := symbolToken(token)
		if !ok || name == found {
			continue
		}
		if found != "" {
			return ""
		}
		found = name
	}
}

// symbolToken is token's identifier when it reads as a code symbol (SymbolIn).
func symbolToken(token string) (string, bool) {
	call := strings.HasSuffix(token, "()")
	name := strings.TrimSuffix(token, "()")
	if name == "" || len(name) > 120 {
		return "", false
	}
	codeLike := call
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '_':
			codeLike = true
		case c >= 'a' && c <= 'z':
			if i+1 < len(name) && name[i+1] >= 'A' && name[i+1] <= 'Z' {
				codeLike = true
			}
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
			if i == 0 {
				return "", false
			}
		default:
			return "", false
		}
	}
	return name, codeLike
}

// Symbol is the symbol a project record's frame names: its one symbols key, else the one symbol
// its text names (SymbolIn); "" for any other record.
func Symbol(o ledger.Observation) string {
	if !IsRecord(o) {
		return ""
	}
	if len(o.Keys.Symbols) > 0 {
		if len(o.Keys.Symbols) == 1 {
			return o.Keys.Symbols[0]
		}
		return ""
	}
	return SymbolIn(o.Content)
}

// relPath is a frame's path as a path of HEAD's tree, "" when it is none (absolute, outside the
// work tree).
func relPath(p string) string {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return ""
	}
	p = path.Clean(p)
	if p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	return p
}

// anchors are the records the view is asked about: project records of repo with a path or a
// symbol, at most MaxRecords, the newest kept, in ledger order.
func anchors(records []ledger.Observation, repo host.Repo) []ledger.Observation {
	var out []ledger.Observation
	for _, o := range records {
		if repo.ID == "" || o.Frame.RepoID != repo.ID || !IsRecord(o) {
			continue
		}
		if relPath(o.Frame.Path) == "" && o.Frame.Symbol == "" {
			continue
		}
		out = append(out, o)
	}
	if len(out) > MaxRecords {
		out = out[len(out)-MaxRecords:]
	}
	return out
}

// Query is what a compaction asks the view about records (the active records it folds): each
// anchored project record's path, its symbol (in its path, or anywhere without one; the newest
// MaxSymbols), and, for one with no body hash, its path's blob at the commit it was framed at.
// Nothing is asked twice; an empty query asks nothing.
func Query(records []ledger.Observation, repo host.Repo) host.StructureQuery {
	var q host.StructureQuery
	paths := map[string]bool{}
	symbols := map[host.SymbolQuery]bool{}
	blobs := map[host.BlobQuery]bool{}
	for _, o := range anchors(records, repo) {
		p := relPath(o.Frame.Path)
		if p != "" && !paths[p] {
			paths[p] = true
			q.Paths = append(q.Paths, p)
		}
		if s := (host.SymbolQuery{Path: p, Symbol: o.Frame.Symbol}); o.Frame.Symbol != "" && !symbols[s] {
			symbols[s] = true
			q.Symbols = append(q.Symbols, s)
		}
		if b := (host.BlobQuery{Path: p, At: o.Frame.Head}); p != "" && o.Frame.BodyHash == "" && o.Frame.Head != "" && !blobs[b] {
			blobs[b] = true
			q.Blobs = append(q.Blobs, b)
		}
	}
	if len(q.Symbols) > MaxSymbols {
		q.Symbols = q.Symbols[len(q.Symbols)-MaxSymbols:]
	}
	return q
}

// Empty reports a query that asks nothing.
func Empty(q host.StructureQuery) bool {
	return len(q.Paths)+len(q.Symbols)+len(q.Blobs)+len(q.Diffs) == 0 && q.List == nil
}

// Verdict is one project record's freshness against HEAD: Gone when its path or symbol no longer
// exists there (Sha is HEAD), Changed when its path's blob differs from the one it was written
// against (Sha is the commit it was written at). An unchanged record, or one the view could not
// tell, has none. A verdict never deletes or supersedes a record.
type Verdict struct {
	ID   string
	Kind string
	Sha  string
	Path string
	// Head is the commit the view answered at; Blob the path's blob there (Changed only).
	Head, Blob string
}

// Text is how the summary and the memory tool print the verdict: "gone at <sha>" or "changed
// since <sha>, unverified".
func (v Verdict) Text() string {
	if v.Kind == Gone {
		return "gone at " + short(v.Sha)
	}
	return "changed since " + short(v.Sha) + ", unverified"
}

func short(sha string) string {
	if len(sha) > ShortSHA {
		return sha[:ShortSHA]
	}
	return sha
}

// Verdicts judges records against the view's answer to Query(records, repo), in record order.
func Verdicts(records []ledger.Observation, repo host.Repo, a host.StructureAnswer) []Verdict {
	if a.Head == "" {
		return nil
	}
	paths := map[string]host.PathAnswer{}
	for _, p := range a.Paths {
		paths[p.Path] = p
	}
	symbols := map[host.SymbolQuery]host.SymbolAnswer{}
	for _, s := range a.Symbols {
		symbols[host.SymbolQuery{Path: s.Path, Symbol: s.Symbol}] = s
	}
	blobs := map[host.BlobQuery]host.BlobAnswer{}
	for _, b := range a.Blobs {
		blobs[host.BlobQuery{Path: b.Path, At: b.At}] = b
	}
	var out []Verdict
	for _, o := range anchors(records, repo) {
		p := relPath(o.Frame.Path)
		pa, hasPath := paths[p]
		gone := p != "" && hasPath && pa.Known && !pa.Exists
		if s, ok := symbols[host.SymbolQuery{Path: p, Symbol: o.Frame.Symbol}]; o.Frame.Symbol != "" && ok && s.Known && !s.Found {
			gone = true
		}
		if gone {
			out = append(out, Verdict{ID: o.ID, Kind: Gone, Sha: a.Head, Path: p, Head: a.Head})
			continue
		}
		if p == "" || !hasPath || !pa.Known || pa.Blob == "" || o.Frame.Head == "" {
			continue
		}
		recorded := o.Frame.BodyHash
		if recorded == "" {
			if o.Frame.Head == a.Head {
				continue
			}
			b, ok := blobs[host.BlobQuery{Path: p, At: o.Frame.Head}]
			if !ok || !b.Known {
				continue
			}
			recorded = b.Blob
		}
		if recorded != pa.Blob {
			out = append(out, Verdict{ID: o.ID, Kind: Changed, Sha: o.Frame.Head, Path: p, Head: a.Head, Blob: pa.Blob})
		}
	}
	return out
}

// Texts are the verdicts' texts by record id (selection.Input.Freshness).
func Texts(verdicts []Verdict) map[string]string {
	if len(verdicts) == 0 {
		return nil
	}
	out := make(map[string]string, len(verdicts))
	for _, v := range verdicts {
		out[v.ID] = v.Text()
	}
	return out
}

// StampQuery asks the view for the paths of the new project records that have no body hash.
func StampQuery(records []ledger.Observation) host.StructureQuery {
	var q host.StructureQuery
	seen := map[string]bool{}
	for _, o := range records {
		p := relPath(o.Frame.Path)
		if !IsRecord(o) || o.Frame.BodyHash != "" || p == "" || seen[p] {
			continue
		}
		seen[p] = true
		q.Paths = append(q.Paths, p)
	}
	return q
}

// Stamp sets each new project record's body hash to its path's blob at HEAD, when the view
// answered at the commit the record was framed at (a record framed before a later commit keeps
// none, and is judged by its path's blob at that commit instead). It reports whether it set any.
func Stamp(records []ledger.Observation, a host.StructureAnswer) bool {
	blobs := map[string]string{}
	for _, p := range a.Paths {
		if p.Known && p.Exists && p.Blob != "" {
			blobs[p.Path] = p.Blob
		}
	}
	set := false
	for i := range records {
		o := &records[i]
		if !IsRecord(*o) || o.Frame.BodyHash != "" || a.Head == "" || o.Frame.Head != a.Head {
			continue
		}
		if blob := blobs[relPath(o.Frame.Path)]; blob != "" {
			o.Frame.BodyHash = blob
			set = true
		}
	}
	return set
}

// DiffQuery asks for the diffs of the Changed verdicts, at most MaxDiffs, for the level-1 input.
func DiffQuery(verdicts []Verdict) host.StructureQuery {
	var q host.StructureQuery
	seen := map[host.DiffQuery]bool{}
	for _, v := range verdicts {
		d := host.DiffQuery{Path: v.Path, From: v.Sha}
		if v.Kind != Changed || seen[d] || len(q.Diffs) >= MaxDiffs {
			continue
		}
		seen[d] = true
		q.Diffs = append(q.Diffs, d)
	}
	return q
}
