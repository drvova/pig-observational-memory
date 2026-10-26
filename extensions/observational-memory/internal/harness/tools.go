package harness

import (
	"sync"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omtool"
)

// Manager runs the memory tool, the one tool the extension registers (docs/code-tool.md). The
// tools the agent defined with the removed query language are retired: the manager registers,
// runs and (de)activates none of them, and keeps only their names (NoteRetired), so calls they
// left on the branch stay memory plumbing (SkipNames). PiG keeps the extension process across
// sessions, so one Manager serves every session.
type Manager struct {
	// Resolve is the curated store, project and session a call acts on; false when curated
	// memory is off (no store: no saved memories, no ledgers, no retired definitions).
	Resolve func(h host.ToolHost) (curated.Call, bool)
	// Info is om.Info's answer for a call's host (commands.StatusFigures, injected by the root
	// package); nil refuses om.Info.
	Info func(h host.ToolHost) func() om.StatusInfo
	// IndexShown is the names the pinned curated index lists; nil means none.
	IndexShown func() []string

	mu sync.Mutex
	// retired are the retired defined queries' names no other tool held when the store was
	// read (NoteRetired, SkipNames); the set only grows.
	retired map[string]bool
	// ns is the process's namespace child.
	ns     *Namespace
	nsOnce sync.Once
}

// NewManager returns a manager that knows no retired name yet.
func NewManager(resolve func(h host.ToolHost) (curated.Call, bool)) *Manager {
	return &Manager{Resolve: resolve, retired: map[string]bool{}}
}

// namespace is the process's namespace child, made on first use.
func (m *Manager) namespace() *Namespace {
	m.nsOnce.Do(func() {
		if m.ns == nil {
			m.ns = &Namespace{}
		}
	})
	return m.ns
}

// Namespace is the process's namespace child: Reset at session_start and session_shutdown,
// Status for /om:status.
func (m *Manager) Namespace() *Namespace { return m.namespace() }

func (m *Manager) resolve(h host.ToolHost) (curated.Call, bool) {
	if m.Resolve == nil {
		return curated.Call{}, false
	}
	return m.Resolve(h)
}

// profileOf is the session model's profile and its ledger (false without one).
func profileOf(h host.ToolHost, call curated.Call, on bool) (Profile, Ledger, bool) {
	key := ModelKey(h.SessionModel())
	if !on || key == "" {
		return Default(key), Ledger{}, false
	}
	l := Ledger{Store: call.Store, Model: key}
	return l.Profile(), l, true
}

// sessionEntries are every entry of the session file, for the off-path fallback
// (docs/memory-v2.md, "Branches"); nil when the host cannot read the file.
func sessionEntries(h host.ToolHost) []ledger.Entry {
	reader, ok := h.(host.SessionFileReader)
	if !ok {
		return nil
	}
	all, err := reader.GetEntries()
	if err != nil {
		return nil
	}
	return all
}

// NoteRetired adds the retired defined queries' names (the store's definitions and the legacy
// files the layer refused, RetiredQueries) to SkipNames, at each session start, except a name
// another tool of the session's registry holds (that tool's calls are conversation) and the
// memory tool's own. It registers nothing, notifies nothing and writes nothing: the retired
// queries are listed by /om:status and om.Retired(). With curated memory off, or the registry
// or the store unreadable, it adds nothing.
func (m *Manager) NoteRetired(h host.ToolHost) {
	call, on := m.resolve(h)
	if !on {
		return
	}
	retired, err := RetiredQueries(call.Store)
	if err != nil || len(retired) == 0 {
		return
	}
	tools, err := h.AllTools()
	if err != nil {
		return
	}
	taken := map[string]bool{}
	for _, t := range tools {
		taken[t.Name] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, q := range retired {
		if q.Name != "" && q.Name != omtool.ToolName && !taken[q.Name] {
			m.retired[q.Name] = true
		}
	}
}
