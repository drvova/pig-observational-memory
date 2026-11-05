// Package neutral runs the memory engine in a harness that owns its own transcript and
// compaction (Claude Code, Codex, OpenCode, …): the engine never writes the harness's files.
// Its own entries (level-0 batches, coverage markers, compactions) live in a Store beside the
// transcript, each placed after the transcript entry that was the tip when it was written, and
// the branch the engine reads is the two merged (Branch).
package neutral

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/filelock"
	"github.com/drvova/pig-observational-memory/memory/session"
)

// Stored is one entry of the store and where it goes on the branch.
type Stored struct {
	// After is the id of the entry it follows: a transcript entry, or another stored entry;
	// "" puts it first.
	After string `json:"after"`
	// Entry is the Pi-shaped entry object (custom or compaction).
	Entry map[string]any `json:"entry"`
}

// Store is the engine's entries for one session: one JSON line per Stored, appended under a
// lock so hooks running at once (a turn end and a compaction) never interleave.
type Store struct {
	Path string
}

// StoreFor is the store of one session of a harness under root (the engine's config root):
// <root>/sessions/<harness>/<session>.jsonl.
func StoreFor(root, harness, sessionID string) Store {
	return Store{Path: filepath.Join(root, "sessions", harness, SafeName(sessionID)+".jsonl")}
}

// SafeName is a session id usable as one file name: path separators and NUL become _, and a
// leading dot is dropped, so an id from a hook payload never names a path outside the store.
func SafeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return '_'
		}
		return r
	}, strings.TrimPrefix(s, "."))
}

// Load reads every stored entry, in the order written; a missing store is empty, and a line
// that does not decode (a write cut off) is skipped.
func (s Store) Load() ([]Stored, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Stored
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		var st Stored
		if json.Unmarshal(scanner.Bytes(), &st) != nil || st.Entry == nil {
			continue
		}
		out = append(out, st)
	}
	return out, scanner.Err()
}

// Append writes entry after the entry with id after.
func (s Store) Append(after string, entry map[string]any) error {
	line, err := json.Marshal(Stored{After: after, Entry: entry})
	if err != nil {
		return fmt.Errorf("neutral store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	return filelock.WithLock(s.Path+".lock", func() error {
		f, err := os.OpenFile(s.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	})
}

// Branch is the transcript with the stored entries placed in: each right after the entry it
// names (stored entries after one entry keep their written order), one naming an entry the
// branch no longer holds (the harness rewrote its transcript) at the end, and parent ids
// relinked to the merged order, so the engine reads one linear branch as it does in Pi.
func Branch(transcript []session.Entry, stored []Stored) []session.Entry {
	after := map[string][]session.Entry{}
	placed := map[string]bool{}
	// alias maps a source id a reader folded into an entry (Aliases) to that entry's id.
	alias := map[string]string{}
	for _, e := range transcript {
		placed[e.ID] = true
		for _, a := range Aliases(e) {
			alias[a] = e.ID
		}
	}
	for _, st := range stored {
		if id, _ := st.Entry["id"].(string); id != "" {
			placed[id] = true
		}
	}
	var orphans []session.Entry
	for _, st := range stored {
		e := session.FromMap(st.Entry)
		if id, ok := alias[st.After]; ok && !placed[st.After] {
			st.After = id
		}
		if st.After != "" && !placed[st.After] {
			orphans = append(orphans, e)
			continue
		}
		after[st.After] = append(after[st.After], e)
	}
	out := make([]session.Entry, 0, len(transcript)+len(stored))
	var emit func(e session.Entry)
	emit = func(e session.Entry) {
		out = append(out, e)
		children := after[e.ID]
		delete(after, e.ID)
		for _, c := range children {
			emit(c)
		}
	}
	for _, c := range after[""] {
		emit(c)
	}
	delete(after, "")
	for _, e := range transcript {
		emit(e)
	}
	for _, e := range orphans {
		emit(e)
	}
	for i := range out {
		parent := ""
		if i > 0 {
			parent = out[i-1].ID
		}
		out[i].ParentID = parent
		if out[i].Raw != nil {
			out[i].Raw["parentId"] = parent
		}
	}
	return out
}

// AliasesKey is the raw member a transcript reader lists, on an entry it built from several
// source lines (an assistant message written as one line per block), the source ids other
// than the entry's own, so a stored entry naming any of them still lands after the entry.
const AliasesKey = "omAliases"

// Aliases are the source ids folded into e (AliasesKey).
func Aliases(e session.Entry) []string {
	if e.Raw == nil {
		return nil
	}
	var out []string
	switch v := e.Raw[AliasesKey].(type) {
	case []string:
		out = v
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
