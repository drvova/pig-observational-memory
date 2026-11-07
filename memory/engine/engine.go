// Package engine is the memory engine's per-process setup every harness adapter shares
// (docs/universal.md): the memory tool's manager over the curated store, what runs at a
// session's start and when the agent settles, and the system-prompt text. An adapter wires
// these to its harness's events; the engine's work per event is in memory/hooks.
package engine

import (
	"github.com/drvova/pig-observational-memory/memory/commands"
	"github.com/drvova/pig-observational-memory/memory/config"
	"github.com/drvova/pig-observational-memory/memory/core"
	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/harness"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/om"
	"github.com/drvova/pig-observational-memory/memory/runtime"
)

// ToolNoteLine is the engine's one system-prompt line asking the session model to say, before
// a tool call, what it is doing and why: level 0 keeps the text before an assistant message's
// first tool call verbatim on that call's record (docs/memory-v2.md, "The model's own words").
// The model writes those words to the user, so the line names no memory, compaction or
// resuming, which the model would otherwise repeat to the user before every call. It costs no
// request.
const ToolNoteLine = "Before each tool call, say in one short sentence what you are doing and why."

// SectionText is the engine's system-prompt section: ToolNoteLine (a fixed line, so the prompt
// prefix stays stable), followed by the curated index when curated memory is on and the index
// is not empty.
func SectionText(index string, curatedOn bool) string {
	if !curatedOn || index == "" {
		return ToolNoteLine
	}
	return ToolNoteLine + "\n\n" + index
}

// CuratedCall is the store, project and session a memory query acts on, the configuration it
// was read under, and whether curated memory is on. Project settings are read only from a
// project the harness trusts.
func CuratedCall(h host.Host) (curated.Call, config.Config, bool) {
	cfg := config.Load(h.Cwd(), config.DefaultPaths(), config.TrustFrom(h.ProjectTrusted()), nil)
	store, on := harness.StoreOf(h, cfg)
	if !on {
		return curated.Call{}, cfg, false
	}
	return curated.Call{Store: store, Project: harness.ProjectRoot(h.Cwd()), Source: h.SessionID()}, cfg, true
}

// SettleCurated runs the curated evidence check (harness.SettleCurated): code only, no request.
// With curated memory off it does nothing.
func SettleCurated(h host.Host) {
	if call, _, on := CuratedCall(h); on {
		_, _ = harness.SettleCurated(h, call)
	}
}

// MigrateLayer migrates the written v1 worker instructions to memory v2 (harness.MigrateLayerV2):
// code only, no request. Without a store it does nothing.
func MigrateLayer(h host.Host) {
	call, cfg, on := CuratedCall(h)
	if !on {
		return
	}
	harness.MigrateLayerV2(h, call.Store, harness.ModelKey(h.SessionModel()), harness.WorkerModelKey(h, cfg))
}

// Index is the curated index for the system prompt through pin ("" with curated memory off),
// and whether curated memory is on.
func Index(h host.Host, pin *curated.Pin) (string, bool) {
	call, cfg, on := CuratedCall(h)
	if !on {
		return "", false
	}
	index, _, _ := pin.Get(func() (string, []string) {
		text, shown := curated.IndexAs(call.Store, call.Project, call.Source, cfg.CuratedMemoryIndexTokens)
		if block := CoreBlock(h, call); block != "" {
			text = block + "\n\n" + text
		}
		return text, shown
	})
	return index, true
}

// CoreView is what the session reads the core as (core.View): the user's scope, its project's
// (by repoId when the host knows the repository, and by its root) and its model.
func CoreView(h host.Host, project string) core.View {
	scopes := []string{core.ScopeUser}
	if reader, ok := h.(host.RepoReader); ok {
		if repo, ok, err := reader.Repo(); err == nil && ok && repo.ID != "" {
			scopes = append(scopes, core.ProjectScope(repo.ID, ""))
		}
	}
	if project != "" {
		scopes = append(scopes, core.ProjectScope("", project))
	}
	return core.View{Scopes: scopes, Model: harness.ModelKey(h.SessionModel())}
}

// CoreBlock is the session's pull of the core log (docs/brain-memory.md, phase 6), made only when
// its start context is rendered (at its start and after each compaction, the pin's renders): the
// consumed-through marker moves to the newest generation, and the block of the user's constraints,
// the open intentions and the lessons from other projects it sees (core.Section) goes first in the
// context; "" when it sees none or the core cannot be read.
func CoreBlock(h host.Host, call curated.Call) string {
	st, err := call.Store.Core().Pull(call.Source)
	if err != nil {
		return ""
	}
	return core.Section(&st, CoreView(h, call.Project))
}

// NewManager is the memory tool's manager over the curated store, answering om.Info with
// commands.Info, and registers what rt reads from it (the skip list, the code status).
func NewManager(rt *runtime.Runtime, pin *curated.Pin) *harness.Manager {
	manager := harness.NewManager(func(h host.ToolHost) (curated.Call, bool) {
		call, _, on := CuratedCall(h)
		return call, on
	})
	manager.Info = func(h host.ToolHost) func() om.StatusInfo { return StatusInfo(rt, h) }
	manager.IndexShown = pin.Shown
	rt.SetSkipTools(manager.SkipNames)
	rt.SetCodeStatus(manager.CodeStatusLines)
	return manager
}

// StatusInfo is the closure a code tool call answers om.Info with: commands.Info over the call's
// host, so om.Info and the status command print one set of figures. An unreadable branch is
// reported in the figures rather than failing the call.
func StatusInfo(rt *runtime.Runtime, h host.Host) func() om.StatusInfo {
	return func() om.StatusInfo {
		info, err := commands.Info(rt, h)
		if err != nil {
			return om.StatusInfo{Lines: []string{"Status unavailable: " + err.Error()}}
		}
		return info
	}
}

// SessionStart is what a new, resumed or compacted conversation runs: the pin is reset, the
// namespace resets (only kept definitions reach the next conversation), a detector pass still
// running is stopped, the retired defined tools' names are read, the v1 layer is migrated and
// the curated evidence is settled. Code only.
func SessionStart(rt *runtime.Runtime, manager *harness.Manager, pin *curated.Pin, h host.ToolHost) {
	pin.Reset()
	manager.Namespace().Reset()
	rt.StopDetectorPass(runtime.DetectorStopSessionEnd)
	manager.NoteRetired(h)
	MigrateLayer(h)
	SettleCurated(h)
}
