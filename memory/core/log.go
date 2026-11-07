package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/drvova/pig-observational-memory/memory/filelock"
	"github.com/drvova/pig-observational-memory/memory/jsonl"
	"github.com/drvova/pig-observational-memory/memory/secrets"
)

// Writer is who wrote a core line: the session model's key (provider/id) and the session.
type Writer struct {
	Model   string `json:"model,omitempty"`
	Session string `json:"session,omitempty"`
}

// Line is one line of the core log (contracts/schemas/core-line.schema.json). Its members are
// written in this order by both engines.
type Line struct {
	// Gen is the line's generation: above every generation the log recorded before it, a torn
	// line's included (NextGen).
	Gen int    `json:"gen"`
	Op  string `json:"op"`
	// ID is the item the line writes: the new item of a promote, supersede or contest, the item a
	// forget or scope line acts on.
	ID string `json:"id"`
	// Of is the item a supersede replaces or a contest stands against.
	Of    string `json:"of,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Key   string `json:"key,omitempty"`
	Text  string `json:"text,omitempty"`
	Scope string `json:"scope,omitempty"`
	Via   string `json:"via,omitempty"`
	// Roots are the evidence roots the item rests on (EvidenceRoot), deduplicated.
	Roots []string `json:"roots,omitempty"`
	// Record is the session record (or curated memory) the item came from.
	Record string `json:"record,omitempty"`
	// Tombstones are a forget's ids: core items and the session records they came from.
	Tombstones []string `json:"tombstones,omitempty"`
	// Base is the generation the writer had read through when it decided the line (its
	// consumed-through marker): what tells a supersede from a concurrent contest.
	Base   int     `json:"base"`
	Writer *Writer `json:"writer,omitempty"`
	Time   string  `json:"time"`
}

// The labels of a folded item (Item.Status).
const (
	StatusBelieved   = "believed"
	StatusSuperseded = "superseded"
	StatusContested  = "contested"
	StatusForgotten  = "forgotten"
)

// Item is one core item as the fold holds it.
type Item struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"`
	Key    string   `json:"key"`
	Text   string   `json:"text"`
	Scope  string   `json:"scope"`
	Via    string   `json:"via"`
	Record string   `json:"record"`
	Roots  []string `json:"roots"`
	Writer Writer   `json:"writer"`
	// Born is the generation that wrote the item; Gen the last that touched it.
	Born   int    `json:"born"`
	Gen    int    `json:"gen"`
	Status string `json:"status"`
	// With are the items it is contested with; ReplacedBy the item that superseded it.
	With       []string `json:"with"`
	ReplacedBy string   `json:"replacedBy"`
}

// Live reports an item still shown: believed or contested.
func (it *Item) Live() bool { return it.Status == StatusBelieved || it.Status == StatusContested }

// State is the fold of the log through a generation.
type State struct {
	// Gen is the highest generation folded; Next the generation the next line takes.
	Gen, Next int
	// Items are in the order they were born.
	Items []*Item
	byID  map[string]*Item
	// Tombstoned are the tombstoned ids (items and records), with the generation of the forget.
	Tombstoned map[string]int
	// Torn counts the lines no reader decodes.
	Torn int
}

// Item is the item id names, nil when none.
func (s *State) Item(id string) *Item { return s.byID[id] }

// Forgotten reports whether a tombstone names id.
func (s *State) Forgotten(id string) bool { _, ok := s.Tombstoned[id]; return ok }

// LiveByKey is the live items of kind, scope and key (NormKey), in birth order.
func (s *State) LiveByKey(kind, scope, key string) []*Item {
	var out []*Item
	for _, it := range s.Items {
		if it.Live() && it.Kind == kind && it.Scope == scope && it.Key == key {
			out = append(out, it)
		}
	}
	return out
}

// Log is the core log of a store directory: <store>/core/log.jsonl, its lock and the
// consumed-through markers beside it.
type Log struct {
	Dir string
	Now func() time.Time
}

// In is the core log under the engine's store directory (the curated store's directory).
func In(storeDir string) Log { return Log{Dir: filepath.Join(storeDir, "core"), Now: time.Now} }

func (l Log) path() string         { return filepath.Join(l.Dir, "log.jsonl") }
func (l Log) consumedPath() string { return filepath.Join(l.Dir, "consumed.jsonl") }
func (l Log) lockPath() string     { return filepath.Join(l.Dir, ".lock") }

func (l Log) now() string {
	now := time.Now
	if l.Now != nil {
		now = l.Now
	}
	return now().UTC().Format(time.RFC3339)
}

// Locked runs fn holding the core's one-writer lock.
func (l Log) Locked(fn func() error) error {
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		return err
	}
	return filelock.WithLock(l.lockPath(), fn)
}

// Read folds the whole log.
func (l Log) Read() (State, error) { return l.ReadThrough(-1) }

// ReadThrough folds the log's lines of generation at most through (every line when through is
// negative). A missing log is empty.
func (l Log) ReadThrough(through int) (State, error) {
	data, err := os.ReadFile(l.path())
	if errors.Is(err, fs.ErrNotExist) {
		data, err = nil, nil
	}
	if err != nil {
		return State{}, err
	}
	return Fold(data, through), nil
}

// tornGen reads the generation a line that does not decode began with ({"gen":N), 0 when none.
func tornGen(line []byte) int {
	rest, ok := bytes.CutPrefix(line, []byte(`{"gen":`))
	if !ok {
		return 0
	}
	n := 0
	for _, c := range rest {
		if c < '0' || c > '9' {
			break
		}
		if n > 1<<40 {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// Fold is the state of log text through a generation (every line when through is negative). A
// line that does not decode is skipped, but the generation it began with, and the count of the
// log's lines, still bound the next generation from below: a torn last line never has its
// generation used again (scenario 16).
func Fold(data []byte, through int) State {
	st := State{byID: map[string]*Item{}, Tombstoned: map[string]int{}}
	lines, highest := 0, 0
	for _, raw := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		lines++
		var line Line
		if json.Unmarshal(raw, &line) != nil || line.Gen <= 0 || !slices.Contains(Ops, line.Op) {
			st.Torn++
			highest = max(highest, tornGen(raw))
			continue
		}
		highest = max(highest, line.Gen)
		if through >= 0 && line.Gen > through {
			continue
		}
		st.apply(line)
	}
	st.Next = max(highest, lines) + 1
	return st
}

func (st *State) apply(line Line) {
	st.Gen = max(st.Gen, line.Gen)
	writer := Writer{}
	if line.Writer != nil {
		writer = *line.Writer
	}
	add := func(status string) *Item {
		if st.Forgotten(line.ID) || (line.Record != "" && st.Forgotten(line.Record)) {
			return nil
		}
		if it := st.byID[line.ID]; it != nil {
			for _, r := range line.Roots {
				if !slices.Contains(it.Roots, r) {
					it.Roots = append(it.Roots, r)
				}
			}
			it.Gen = line.Gen
			return it
		}
		it := &Item{ID: line.ID, Kind: line.Kind, Key: line.Key, Text: line.Text, Scope: line.Scope, Via: line.Via, Record: line.Record,
			Roots: slices.Clone(line.Roots), Writer: writer, Born: line.Gen, Gen: line.Gen, Status: status}
		st.Items = append(st.Items, it)
		st.byID[it.ID] = it
		return it
	}
	switch line.Op {
	case OpPromote:
		add(StatusBelieved)
	case OpSupersede:
		old := st.byID[line.Of]
		if it := add(StatusBelieved); it != nil && old != nil && old != it {
			old.Status, old.ReplacedBy, old.Gen = StatusSuperseded, it.ID, line.Gen
			for _, w := range old.With {
				// The other side of a pair the superseded item stood in stands alone again
				// unless another pair still holds it.
				if other := st.byID[w]; other != nil && other.Status == StatusContested {
					other.With = slices.DeleteFunc(other.With, func(s string) bool { return s == old.ID })
					if len(other.With) == 0 {
						other.Status = StatusBelieved
					}
				}
			}
			old.With = nil
		}
	case OpContest:
		old := st.byID[line.Of]
		if it := add(StatusContested); it != nil && old != nil && old != it && old.Live() {
			old.Status, old.Gen = StatusContested, line.Gen
			if !slices.Contains(old.With, it.ID) {
				old.With = append(old.With, it.ID)
			}
			if !slices.Contains(it.With, old.ID) {
				it.With = append(it.With, old.ID)
			}
			it.Status = StatusContested
		}
	case OpForget:
		for _, id := range append([]string{line.ID}, line.Tombstones...) {
			if id == "" {
				continue
			}
			if _, on := st.Tombstoned[id]; !on {
				st.Tombstoned[id] = line.Gen
			}
		}
		for _, it := range st.Items {
			if st.Forgotten(it.ID) || (it.Record != "" && st.Forgotten(it.Record)) {
				it.Status, it.Text, it.Gen = StatusForgotten, "", line.Gen
				if _, on := st.Tombstoned[it.ID]; !on {
					st.Tombstoned[it.ID] = line.Gen
				}
				for _, w := range it.With {
					if other := st.byID[w]; other != nil && other.Status == StatusContested {
						other.With = slices.DeleteFunc(other.With, func(s string) bool { return s == it.ID })
						if len(other.With) == 0 {
							other.Status = StatusBelieved
						}
					}
				}
				it.With = nil
			}
		}
	case OpScope:
		if it := st.byID[line.ID]; it != nil && line.Scope != "" {
			it.Scope, it.Gen = line.Scope, line.Gen
		}
	}
}

// Encode is a line as both engines write it: encoding/json of Line, its text redacted
// (contracts/secrets.md).
func Encode(line Line) ([]byte, error) {
	line.Text = secrets.Redact(line.Text)
	line.Key = secrets.Redact(line.Key)
	return json.Marshal(line)
}

// Commit runs decide on the log as it stands, under the one-writer lock: each line decide emits
// is numbered from the next generation, stamped with the time, appended and folded at once, so
// the next decision reads it. What one writer decides is always decided against the newest
// state, so a second writer's change is re-evaluated, never overwritten unseen. It returns the
// lines written, as stored.
func (l Log) Commit(decide func(st *State, emit func(Line))) ([]Line, error) {
	var out []Line
	err := l.Locked(func() error {
		st, err := l.Read()
		if err != nil {
			return err
		}
		var failed error
		decide(&st, func(line Line) {
			if failed != nil {
				return
			}
			line.Gen = st.Next
			line.Time = l.now()
			raw, err := Encode(line)
			if err == nil {
				_, err = jsonl.Append(l.path(), raw)
			}
			if err != nil {
				failed = err
				return
			}
			var stored Line
			_ = json.Unmarshal(raw, &stored)
			st.apply(stored)
			st.Next++
			out = append(out, stored)
		})
		return failed
	})
	return out, err
}

// Marker is one consumed-through line (contracts/schemas/core-consumed.schema.json): the
// generation a session read the core through, at its start or a compaction.
type Marker struct {
	Session string `json:"session"`
	Through int    `json:"through"`
	Time    string `json:"time"`
}

// Consumed is the generation session last read the core through; ok is false when it never read
// it.
func (l Log) Consumed(session string) (int, bool) {
	data, err := os.ReadFile(l.consumedPath())
	if err != nil {
		return 0, false
	}
	through, ok := 0, false
	for _, raw := range bytes.Split(data, []byte("\n")) {
		var m Marker
		if json.Unmarshal(raw, &m) == nil && m.Session == session && m.Session != "" {
			through, ok = m.Through, true
		}
	}
	return through, ok
}

// Pull is a session's read of the core at its start or a compaction: the state through the newest
// generation, with the session's consumed-through marker moved to it (a marker line is appended
// only when the generation moved). Between two pulls the session reads ReadThrough(Consumed).
func (l Log) Pull(session string) (State, error) {
	var st State
	err := l.Locked(func() error {
		var err error
		if st, err = l.Read(); err != nil {
			return err
		}
		if session == "" {
			return nil
		}
		if through, ok := l.Consumed(session); ok && through == st.Gen {
			return nil
		}
		raw, err := json.Marshal(Marker{Session: session, Through: st.Gen, Time: l.now()})
		if err != nil {
			return err
		}
		_, err = jsonl.Append(l.consumedPath(), raw)
		return err
	})
	return st, err
}

// View is the state session reads between pulls: the core through its consumed-through marker,
// or the whole log when it never pulled.
func (l Log) View(session string) (State, error) {
	if through, ok := l.Consumed(session); ok {
		return l.ReadThrough(through)
	}
	return l.Read()
}

// Exists reports whether the store holds a core log.
func (l Log) Exists() bool {
	_, err := os.Stat(l.path())
	return err == nil
}
