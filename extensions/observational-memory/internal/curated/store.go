// Package curated is the agent's own long-lived memory, after nodecode's
// define-memory: the agent saves a memory when the user corrects it, states a
// preference or teaches it something worth keeping, and edits or forgets it
// when it goes stale. Observational memory records what happened in one
// session; curated memories carry across sessions.
//
// A memory is one file named after it, under a store directory: front matter
// with its type, one-sentence description, project scope, sources and times,
// then an optional body saying why it matters and how to apply it. A usage
// ledger (usage.jsonl) logs every view, save, edit and helped or harmed
// sighting; the index ranks memories by that evidence.
package curated

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
)

// The memory types: the operator's own word (user, feedback) ranks first.
const (
	TypeUser      = "user"
	TypeFeedback  = "feedback"
	TypeProject   = "project"
	TypeReference = "reference"
	TypeLesson    = "lesson"
)

// Types lists the valid memory types in rank order.
var Types = []string{TypeUser, TypeFeedback, TypeProject, TypeReference, TypeLesson}

// Memory is one curated memory.
type Memory struct {
	Name        string
	Type        string
	Description string
	Body        string
	// Project is the project root the memory applies to; "" means everywhere.
	Project string
	// Sources are the sessions that saved or edited the memory.
	Sources []string
	Created time.Time
	Updated time.Time
	// Staged marks a memory saved under memory v2 without a user's confirmation
	// (docs/memory-v2.md, "Curated memory"): saved lists it only in the sessions in
	// StagedBy, the index leaves it out, and code promotes it on evidence (Store.Settle).
	Staged bool
	// StagedBy are the sessions that staged this name with this description (normalized
	// whitespace and case); two of them are evidence.
	StagedBy []string
	// Pinned is set only by the user, by writing pinned: true in the memory's file: no write
	// of the memory tool or of level 1 sets or clears it, and a correction never flags it.
	Pinned bool
	// Retired is when a forget retired the memory under memory v2; a retired memory is
	// listed and found nowhere, and its name may be saved again.
	Retired time.Time
}

// Store is a directory of memories and its usage ledger.
type Store struct {
	Dir string
	// Now is the clock; tests replace it.
	Now func() time.Time
	// Floor is the kernel floor the store's layer checks definitions under; nil is engaged.
	Floor *kernel.Floor
	// V2 applies memory v2's curated rules (docs/memory-v2.md, "Curated memory"): the one
	// validator (Check) on every write and load, and forget retiring instead of removing.
	V2 bool
}

// New returns the store at dir.
func New(dir string) Store { return Store{Dir: dir, Now: time.Now} }

// Layer is the model-built layer kept in the store (docs/memory-interface.md, section 8):
// <store>/harness, locked with the store's lock and checked under its floor.
func (s Store) Layer() layer.Store {
	return layer.Store{Dir: filepath.Join(s.Dir, "harness"), Lock: s, Floor: s.Floor, Now: s.Now}
}

// DefaultDir is the store under the PiG config root.
func DefaultDir(configHome string) string {
	return filepath.Join(configHome, "observational-memory", "memories")
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidateName reports why name cannot name a memory, or nil.
func ValidateName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("memory name %q must be lowercase letters, digits and hyphens, starting with a letter or digit, at most 64 characters", name)
	}
	return nil
}

// ValidType reports whether t is a memory type.
func ValidType(t string) bool {
	for _, v := range Types {
		if v == t {
			return true
		}
	}
	return false
}

func (s Store) path(name string) string { return filepath.Join(s.Dir, name+".md") }

// ErrNotFound is returned for a memory name the store does not hold.
var ErrNotFound = errors.New("no memory by that name")

// Get reads one memory. Under memory v2 a file that fails the validator (fields no memory
// takes, or Check) is refused: it is not loaded, and List reports it (Refused).
func (s Store) Get(name string) (Memory, error) {
	m, unknown, err := s.read(name)
	if err != nil {
		return Memory{}, err
	}
	if err := s.loadCheck(m, unknown); err != nil {
		return Memory{}, err
	}
	return m, nil
}

// read parses one memory file without memory v2's load check, with the front matter keys no
// memory takes: what Update and Forget start from, so a memory the validator refuses can
// still be brought within it or retired.
func (s Store) read(name string) (Memory, []string, error) {
	if err := ValidateName(name); err != nil {
		return Memory{}, nil, err
	}
	data, err := os.ReadFile(s.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return Memory{}, nil, ErrNotFound
	}
	if err != nil {
		return Memory{}, nil, err
	}
	return parse(name, string(data))
}

// loadCheck is memory v2's validator on a load: no fields a memory does not take (unknown),
// and Check. It passes everything under v1.
func (s Store) loadCheck(m Memory, unknown []string) error {
	if !s.V2 {
		return nil
	}
	if len(unknown) > 0 {
		return fmt.Errorf("memory %q has fields no memory takes: %s", m.Name, strings.Join(unknown, ", "))
	}
	return Check(m)
}

// RefusedMemory is a memory file the validator refuses on load (memory v2), and why.
type RefusedMemory struct {
	Name, Reason string
}

// Refused lists the memory files that apply in project (or whose project cannot be read) and
// are not retired, but that Get refuses: under memory v2, the ones failing the validator, which
// List leaves out. A forget retires such a memory, and an edit that brings it within the
// validator makes it readable again. None under v1 but unparseable files.
func (s Store) Refused(project string) ([]RefusedMemory, error) {
	names, err := s.fileNames()
	if err != nil {
		return nil, err
	}
	out := []RefusedMemory{}
	for _, name := range names {
		m, unknown, err := s.read(name)
		if err == nil {
			if !Visible(m, project) || !m.Retired.IsZero() {
				continue
			}
			err = s.loadCheck(m, unknown)
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			out = append(out, RefusedMemory{Name: name, Reason: err.Error()})
		}
	}
	return out, nil
}

// Visible reports whether m applies in project: it is global or scoped to it.
func Visible(m Memory, project string) bool { return m.Project == "" || m.Project == project }

// GetIn reads one memory as seen from project: a memory scoped to another
// project, or retired, is not found there.
func (s Store) GetIn(name, project string) (Memory, error) {
	m, err := s.Get(name)
	if err != nil {
		return Memory{}, err
	}
	if !Visible(m, project) || !m.Retired.IsZero() {
		return Memory{}, ErrNotFound
	}
	return m, nil
}

// GetAs reads one memory as session sees it from project: GetIn, and a staged memory only in
// the sessions that staged it.
func (s Store) GetAs(name, project, session string) (Memory, error) {
	m, err := s.GetIn(name, project)
	if err != nil {
		return Memory{}, err
	}
	if !SeenBy(m, session) {
		return Memory{}, ErrNotFound
	}
	return m, nil
}

// readIn is GetIn without memory v2's load check (read): what Update and Forget start from.
func (s Store) readIn(name, project string) (Memory, error) {
	m, _, err := s.read(name)
	if err != nil {
		return Memory{}, err
	}
	if !Visible(m, project) || !m.Retired.IsZero() {
		return Memory{}, ErrNotFound
	}
	return m, nil
}

// SeenBy reports whether session sees m: a live memory everywhere, a staged one only in the
// sessions that staged it.
func SeenBy(m Memory, session string) bool {
	return !m.Staged || slices.Contains(m.StagedBy, session)
}

// List returns the memories that apply in project (global ones and those
// scoped to it), in name order, staged ones included. Unreadable files and
// retired memories are skipped; Refused lists the files the validator refuses.
func (s Store) List(project string) ([]Memory, error) {
	names, err := s.fileNames()
	if err != nil {
		return nil, err
	}
	out := []Memory{}
	for _, name := range names {
		m, err := s.Get(name)
		if err != nil {
			continue
		}
		if Visible(m, project) && m.Retired.IsZero() {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// fileNames are the memory names the store directory holds files for, in name order.
func (s Store) fileNames() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".md")
		if ok && !entry.IsDir() && ValidateName(name) == nil {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Save writes a new memory. A name already in use is refused: a name has one
// home, so a changed fact is an edit of the existing memory, not a second one.
func (s Store) Save(m Memory, source string) (Memory, error) {
	if err := ValidateName(m.Name); err != nil {
		return Memory{}, err
	}
	if !ValidType(m.Type) {
		return Memory{}, fmt.Errorf("memory type %q must be one of %s", m.Type, strings.Join(Types, ", "))
	}
	if strings.TrimSpace(m.Description) == "" {
		return Memory{}, errors.New("a memory needs a one-sentence description")
	}
	m.Pinned, m.Retired = false, time.Time{}
	if !m.Staged {
		m.StagedBy = nil
	}
	var saved Memory
	joined := false
	err := s.locked(func() error {
		if existing, err := s.Get(m.Name); err == nil && existing.Retired.IsZero() {
			if existing.Staged && m.Staged && Visible(existing, m.Project) && sameDescription(existing.Description, m.Description) {
				// The same name and description staged in another session: evidence the
				// next Settle reads.
				existing.StagedBy = addSource(existing.StagedBy, source)
				existing.Sources = addSource(existing.Sources, source)
				existing.Updated = s.Now().UTC()
				if err := s.writeChecked(existing); err != nil {
					return err
				}
				saved, joined = existing, true
				return nil
			}
			if existing.Staged && !SeenBy(existing, source) {
				return fmt.Errorf("the name %q is staged by another session with another description; choose another name", m.Name)
			}
			if !Visible(existing, m.Project) {
				return fmt.Errorf("the name %q is taken by a memory scoped to another project; choose another name", m.Name)
			}
			return fmt.Errorf("a memory named %q already exists; change it with edit name=%s instead", m.Name, m.Name)
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			if _, _, readErr := s.read(m.Name); readErr == nil {
				// Readable but refused by the validator: an edit can bring it within it.
				return fmt.Errorf("the name %q is taken by a memory the validator refuses (%v); fix it with edit name=%s or retire it with forget name=%s", m.Name, err, m.Name, m.Name)
			}
			return fmt.Errorf("the name %q is taken by an unreadable memory file; choose another name", m.Name)
		}
		if err := s.migrateDefinitionsLocked(); err != nil {
			return err
		}
		if _, err := os.Stat(s.definitionPath(m.Name)); err == nil {
			return fmt.Errorf("the name %q is taken by a defined query; choose another name", m.Name)
		}
		now := s.Now().UTC()
		m.Description = oneLine(m.Description)
		m.Created, m.Updated = now, now
		m.Sources = addSource(nil, source)
		if m.Staged {
			m.StagedBy = addSource(nil, source)
		}
		saved = m
		return s.writeChecked(m)
	})
	if err != nil {
		return Memory{}, err
	}
	if joined {
		return saved, s.Record(m.Name, EventStaged)
	}
	return saved, s.Record(m.Name, EventSave)
}

// Edit is a change to a memory; nil fields are left as they are.
type Edit struct {
	Type        *string
	Description *string
	Body        *string
}

// Update applies edit to the memory name, as seen from project. It reads the memory without
// memory v2's load check, so an edit can bring a memory the validator refuses within it (a
// rewrite keeps only the fields a memory takes); the edited memory is checked as every write is.
func (s Store) Update(name, project string, edit Edit, source string) (Memory, error) {
	var updated Memory
	err := s.locked(func() error {
		m, err := s.readIn(name, project)
		if err != nil {
			return err
		}
		if !SeenBy(m, source) {
			return ErrNotFound
		}
		if edit.Type != nil {
			if !ValidType(*edit.Type) {
				return fmt.Errorf("memory type %q must be one of %s", *edit.Type, strings.Join(Types, ", "))
			}
			m.Type = *edit.Type
		}
		if edit.Description != nil {
			if strings.TrimSpace(*edit.Description) == "" {
				return errors.New("a memory needs a one-sentence description")
			}
			if m.Staged && !sameDescription(m.Description, *edit.Description) {
				// Another session's staging named the old description.
				m.StagedBy = addSource(nil, source)
			}
			m.Description = oneLine(*edit.Description)
		}
		if edit.Body != nil {
			m.Body = strings.TrimSpace(*edit.Body)
		}
		m.Updated = s.Now().UTC()
		m.Sources = addSource(m.Sources, source)
		updated = m
		return s.writeChecked(m)
	})
	if err != nil {
		return Memory{}, err
	}
	return updated, s.Record(name, EventEdit)
}

// Forget removes the memory name, as seen from project; under memory v2 it
// retires it instead (Memory.Retired), keeping the file. Its usage history
// stays in the ledger. It reads the memory without memory v2's load check, so a memory the
// validator refuses can still be retired.
func (s Store) Forget(name, project string) error {
	err := s.locked(func() error {
		m, err := s.readIn(name, project)
		if err != nil {
			return err
		}
		if s.V2 {
			m.Retired = s.Now().UTC()
			return s.write(m)
		}
		return os.Remove(s.path(name))
	})
	if err != nil {
		return err
	}
	return s.Record(name, EventForget)
}

// The usage ledger's events.
const (
	EventView    = "view"
	EventSave    = "save"
	EventEdit    = "edit"
	EventForget  = "forget"
	EventHelped  = "helped"
	EventHarmed  = "harmed"
	EventIndexed = "indexed"
	// EventStaged is a save that staged a memory's name and description in one more session
	// (memory v2).
	EventStaged = "staged"
	// EventCited is a level-1 citation of a memory (memory v2): evidence for a staged one.
	EventCited = "cited"
	// EventCorrected is a level-0 user correction whose subject matches the memory (memory
	// v2): it ranks the memory lower and flags it until the memory is edited.
	EventCorrected = "corrected"
	// EventPromoted is a staged memory made live on evidence (memory v2).
	EventPromoted = "promoted"
)

type usageLine struct {
	Time  string `json:"time"`
	Name  string `json:"name"`
	Event string `json:"event"`
	// Quote is a helped or harmed event's evidence (NormalizeQuote).
	Quote string `json:"quote,omitempty"`
	// User marks a helped event whose quote is a user's words (memory v2's evidence).
	User bool `json:"user,omitempty"`
	// Record is the record id a corrected event names, or what a cited or promoted event
	// rests on.
	Record string `json:"record,omitempty"`
}

func (s Store) usagePath() string { return filepath.Join(s.Dir, "usage.jsonl") }

// Record appends one usage event for name.
func (s Store) Record(name, event string) error {
	return s.appendUsage(usageLine{Name: name, Event: event})
}

// RecordFeedback appends a helped or harmed event for name with the quote
// that evidences it.
func (s Store) RecordFeedback(name, outcome, quote string) error {
	if outcome != EventHelped && outcome != EventHarmed {
		return fmt.Errorf("outcome %q must be helped or harmed", outcome)
	}
	return s.appendUsage(usageLine{Name: name, Event: outcome, Quote: quote})
}

func (s Store) appendUsage(entry usageLine) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	entry.Time = s.Now().UTC().Format(time.RFC3339)
	line, _ := json.Marshal(entry)
	f, err := os.OpenFile(s.usagePath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// Evidence is a memory's usage counts.
type Evidence struct {
	Views, Helped, Harmed, Edits int
	// Corrected counts the corrected events since the memory was last saved or edited: a
	// memory with any is flagged and ranks after the others (memory v2).
	Corrected int
	Last      time.Time
}

// Usage reads the ledger into per-memory evidence. A missing ledger is empty.
func (s Store) Usage() (map[string]Evidence, error) {
	lines, err := s.readUsageLines()
	if err != nil {
		return nil, err
	}
	out := map[string]Evidence{}
	for _, line := range lines {
		e := out[line.Name]
		switch line.Event {
		case EventView:
			e.Views++
		case EventHelped:
			e.Helped++
		case EventHarmed:
			e.Harmed++
		case EventEdit:
			e.Edits++
			e.Corrected = 0
		case EventSave:
			e.Corrected = 0
		case EventCorrected:
			e.Corrected++
		case EventForget:
			e = Evidence{}
		}
		if t, err := time.Parse(time.RFC3339, line.Time); err == nil && t.After(e.Last) {
			e.Last = t
		}
		out[line.Name] = e
	}
	return out, nil
}

// Locked runs fn holding the store's exclusive lock, the one every write to the store takes.
// Packages that keep their own files in the store directory (the harness's rules) write them
// under it. fn must not call a Store method that takes the lock itself.
func (s Store) Locked(fn func() error) error { return s.locked(fn) }

// --- files ---------------------------------------------------------------------

// writeChecked writes m after the store's validator (Check under memory v2).
func (s Store) writeChecked(m Memory) error {
	if s.V2 {
		if err := Check(m); err != nil {
			return err
		}
	}
	return s.write(m)
}

func (s Store) write(m Memory) error {
	tmp := s.path(m.Name) + ".tmp"
	if err := os.WriteFile(tmp, []byte(format(m)), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(m.Name))
}

func format(m Memory) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "type: %s\n", m.Type)
	fmt.Fprintf(&b, "description: %s\n", m.Description)
	if m.Project != "" {
		fmt.Fprintf(&b, "project: %s\n", m.Project)
	}
	if len(m.Sources) > 0 {
		fmt.Fprintf(&b, "sources: %s\n", strings.Join(m.Sources, ", "))
	}
	fmt.Fprintf(&b, "created: %s\n", m.Created.Format(time.RFC3339))
	fmt.Fprintf(&b, "updated: %s\n", m.Updated.Format(time.RFC3339))
	if m.Staged {
		b.WriteString("staged: true\n")
		if len(m.StagedBy) > 0 {
			fmt.Fprintf(&b, "staged-by: %s\n", strings.Join(m.StagedBy, ", "))
		}
	}
	if m.Pinned {
		b.WriteString("pinned: true\n")
	}
	if !m.Retired.IsZero() {
		fmt.Fprintf(&b, "retired: %s\n", m.Retired.Format(time.RFC3339))
	}
	b.WriteString("---\n")
	if m.Body != "" {
		b.WriteString(m.Body)
		b.WriteString("\n")
	}
	return b.String()
}

// parse reads a memory file; unknown are the front matter keys no memory takes, which only
// memory v2's validator refuses.
func parse(name, text string) (m Memory, unknown []string, err error) {
	m = Memory{Name: name}
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return Memory{}, nil, fmt.Errorf("memory %q has no front matter", name)
	}
	header, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		header, ok = strings.CutSuffix(rest, "\n---")
		if !ok {
			return Memory{}, nil, fmt.Errorf("memory %q has unterminated front matter", name)
		}
	}
	for _, line := range strings.Split(header, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "type":
			m.Type = value
		case "description":
			m.Description = value
		case "project":
			m.Project = value
		case "sources":
			for _, s := range strings.Split(value, ",") {
				if s = strings.TrimSpace(s); s != "" {
					m.Sources = append(m.Sources, s)
				}
			}
		case "created":
			m.Created, _ = time.Parse(time.RFC3339, value)
		case "updated":
			m.Updated, _ = time.Parse(time.RFC3339, value)
		case "staged":
			m.Staged = value == "true"
		case "staged-by":
			for _, s := range strings.Split(value, ",") {
				if s = strings.TrimSpace(s); s != "" {
					m.StagedBy = append(m.StagedBy, s)
				}
			}
		case "pinned":
			m.Pinned = value == "true"
		case "retired":
			m.Retired, _ = time.Parse(time.RFC3339, value)
		default:
			unknown = append(unknown, strings.TrimSpace(key))
		}
	}
	m.Body = strings.TrimSpace(body)
	if !ValidType(m.Type) || m.Description == "" {
		return Memory{}, nil, fmt.Errorf("memory %q needs a valid type and a description", name)
	}
	return m, unknown, nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func addSource(sources []string, source string) []string {
	if source == "" {
		return sources
	}
	for _, s := range sources {
		if s == source {
			return sources
		}
	}
	return append(sources, source)
}
