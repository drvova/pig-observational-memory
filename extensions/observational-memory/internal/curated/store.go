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
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
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
}

// Store is a directory of memories and its usage ledger.
type Store struct {
	Dir string
	// Now is the clock; tests replace it.
	Now func() time.Time
}

// New returns the store at dir.
func New(dir string) Store { return Store{Dir: dir, Now: time.Now} }

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

// Get reads one memory.
func (s Store) Get(name string) (Memory, error) {
	if err := ValidateName(name); err != nil {
		return Memory{}, err
	}
	data, err := os.ReadFile(s.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return Memory{}, ErrNotFound
	}
	if err != nil {
		return Memory{}, err
	}
	return parse(name, string(data))
}

// Visible reports whether m applies in project: it is global or scoped to it.
func Visible(m Memory, project string) bool { return m.Project == "" || m.Project == project }

// GetIn reads one memory as seen from project: a memory scoped to another
// project is not found there.
func (s Store) GetIn(name, project string) (Memory, error) {
	m, err := s.Get(name)
	if err != nil {
		return Memory{}, err
	}
	if !Visible(m, project) {
		return Memory{}, ErrNotFound
	}
	return m, nil
}

// List returns the memories that apply in project (global ones and those
// scoped to it), in name order. Unreadable files are skipped.
func (s Store) List(project string) ([]Memory, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Memory{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Memory{}
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".md")
		if !ok || entry.IsDir() || ValidateName(name) != nil {
			continue
		}
		m, err := s.Get(name)
		if err != nil {
			continue
		}
		if Visible(m, project) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
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
	var saved Memory
	err := s.locked(func() error {
		if existing, err := s.Get(m.Name); err == nil {
			if !Visible(existing, m.Project) {
				return fmt.Errorf("the name %q is taken by a memory scoped to another project; choose another name", m.Name)
			}
			return fmt.Errorf("a memory named %q already exists; edit it with memory_edit instead", m.Name)
		} else if _, statErr := os.Stat(s.path(m.Name)); statErr == nil {
			return fmt.Errorf("the name %q is taken by an unreadable memory file; choose another name", m.Name)
		}
		now := s.Now().UTC()
		m.Description = oneLine(m.Description)
		m.Created, m.Updated = now, now
		m.Sources = addSource(nil, source)
		saved = m
		return s.write(m)
	})
	if err != nil {
		return Memory{}, err
	}
	return saved, s.Record(m.Name, EventSave)
}

// Edit is a change to a memory; nil fields are left as they are.
type Edit struct {
	Type        *string
	Description *string
	Body        *string
}

// Update applies edit to the memory name, as seen from project.
func (s Store) Update(name, project string, edit Edit, source string) (Memory, error) {
	var updated Memory
	err := s.locked(func() error {
		m, err := s.GetIn(name, project)
		if err != nil {
			return err
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
			m.Description = oneLine(*edit.Description)
		}
		if edit.Body != nil {
			m.Body = strings.TrimSpace(*edit.Body)
		}
		m.Updated = s.Now().UTC()
		m.Sources = addSource(m.Sources, source)
		updated = m
		return s.write(m)
	})
	if err != nil {
		return Memory{}, err
	}
	return updated, s.Record(name, EventEdit)
}

// Forget removes the memory name, as seen from project. Its usage history
// stays in the ledger.
func (s Store) Forget(name, project string) error {
	err := s.locked(func() error {
		if _, err := s.GetIn(name, project); err != nil {
			return err
		}
		return os.Remove(s.path(name))
	})
	if err != nil {
		return err
	}
	return s.Record(name, EventForget)
}

// Search returns the memories in project whose name, description or body
// contains every word of query, case-insensitively.
func (s Store) Search(project, query string) ([]Memory, error) {
	all, err := s.List(project)
	if err != nil {
		return nil, err
	}
	words := strings.Fields(strings.ToLower(query))
	out := []Memory{}
	for _, m := range all {
		hay := strings.ToLower(m.Name + "\n" + m.Description + "\n" + m.Body)
		match := len(words) > 0
		for _, w := range words {
			if !strings.Contains(hay, w) {
				match = false
				break
			}
		}
		if match {
			out = append(out, m)
		}
	}
	return out, nil
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
)

type usageLine struct {
	Time  string `json:"time"`
	Name  string `json:"name"`
	Event string `json:"event"`
}

func (s Store) usagePath() string { return filepath.Join(s.Dir, "usage.jsonl") }

// Record appends one usage event for name.
func (s Store) Record(name, event string) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	line, _ := json.Marshal(usageLine{Time: s.Now().UTC().Format(time.RFC3339), Name: name, Event: event})
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
	Last                         time.Time
}

// Usage reads the ledger into per-memory evidence. A missing ledger is empty.
func (s Store) Usage() (map[string]Evidence, error) {
	out := map[string]Evidence{}
	f, err := os.Open(s.usagePath())
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		var line usageLine
		if json.Unmarshal(scanner.Bytes(), &line) != nil || line.Name == "" {
			continue
		}
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
		case EventForget:
			e = Evidence{}
		}
		if t, err := time.Parse(time.RFC3339, line.Time); err == nil && t.After(e.Last) {
			e.Last = t
		}
		out[line.Name] = e
	}
	return out, scanner.Err()
}

// --- files ---------------------------------------------------------------------

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
	b.WriteString("---\n")
	if m.Body != "" {
		b.WriteString(m.Body)
		b.WriteString("\n")
	}
	return b.String()
}

func parse(name, text string) (Memory, error) {
	m := Memory{Name: name}
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return Memory{}, fmt.Errorf("memory %q has no front matter", name)
	}
	header, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		header, ok = strings.CutSuffix(rest, "\n---")
		if !ok {
			return Memory{}, fmt.Errorf("memory %q has unterminated front matter", name)
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
		}
	}
	m.Body = strings.TrimSpace(body)
	if !ValidType(m.Type) || m.Description == "" {
		return Memory{}, fmt.Errorf("memory %q needs a valid type and a description", name)
	}
	return m, nil
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
