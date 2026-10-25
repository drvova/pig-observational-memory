package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

// Corrections are the level-0 user corrections on the branch (docs/memory-v2.md, "Curated
// memory"): records of kind correction that code picked (level 0 picks them from user messages
// only), retractions left out, each timed by its first source entry on the branch.
func Corrections(entries []ledger.Entry) []curated.Correction {
	folded := ledger.FoldLedger(entries, "")
	byID := make(map[string]ledger.Entry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	out := []curated.Correction{}
	for _, o := range folded.Observations {
		if o.Kind != "correction" || o.Origin != ledger.OriginCode || o.Status == ledger.StatusRetracted || len(o.SourceEntryIDs) == 0 {
			continue
		}
		entry, ok := byID[o.SourceEntryIDs[0]]
		if !ok {
			continue
		}
		var stamp any = entry.Timestamp
		if entry.Message != nil && entry.Message["timestamp"] != nil {
			stamp = entry.Message["timestamp"]
		}
		when, ok := serialize.ParseTimestamp(stamp)
		if !ok {
			continue
		}
		out = append(out, curated.Correction{ID: o.ID, Subject: o.Subject, Time: when})
	}
	return out
}

// SettleCurated runs memory v2's curated evidence check for call's session (curated.Store.Settle)
// with the user corrections on h's branch, and tells the user of each memory it made live and
// each one a correction flagged. It makes no request. The index is not re-rendered: a promoted
// memory enters it at its next render (session start or after a compaction).
func SettleCurated(h host.Host, call curated.Call) (curated.Settled, error) {
	entries, err := h.GetBranch()
	if err != nil {
		return curated.Settled{}, err
	}
	settled, err := call.Store.Settle(curated.SettleInput{Project: call.Project, Corrections: Corrections(entries)})
	NotifySettled(h, settled)
	return settled, err
}

// NotifySettled tells the user what one Settle changed: a live memory reaches later system
// prompts, so each promotion is shown, as each save is.
func NotifySettled(h host.Host, settled curated.Settled) {
	for _, p := range settled.Promoted {
		h.Notify(fmt.Sprintf("Observational memory: staged memory %s is now live (%s); it enters the memory index at its next render", p.Name, p.Evidence), "info")
	}
	if len(settled.Flagged) > 0 {
		h.Notify("Observational memory: a correction matches the memories "+strings.Join(settled.Flagged, ", ")+"; they rank last and are flagged in saved until edited", "info")
	}
}

// CuratedStatusLines are /om:status's curated memory lines under memory v2: live, staged,
// pinned and flagged memories in this project, and the files the validator refuses
// (curated.Store.Refused); none under v1 or without a store.
func CuratedStatusLines(h host.Host, cfg config.Config) []string {
	if !cfg.V2() {
		return nil
	}
	project := ProjectRoot(h.Cwd())
	store, ok := StoreOf(h, cfg)
	if !ok {
		return nil
	}
	memories, err := store.List(project)
	if err != nil {
		return []string{"── Curated memory ──", "Store unreadable: " + err.Error()}
	}
	usage := store.RankUsage()
	live, staged, pinned := 0, 0, 0
	flagged := []string{}
	for _, m := range memories {
		switch {
		case m.Staged:
			staged++
		default:
			live++
		}
		if m.Pinned {
			pinned++
		}
		if curated.Flagged(m, usage[m.Name]) {
			flagged = append(flagged, m.Name)
		}
	}
	lines := []string{"── Curated memory ──", fmt.Sprintf("Memories: %d live (%d pinned), %d staged (out of the index until evidence confirms them)", live, pinned, staged)}
	if len(flagged) > 0 {
		lines = append(lines, "Flagged by a user correction since their last edit (ranked last): "+strings.Join(flagged, ", "))
	}
	if refused, err := store.Refused(project); err == nil && len(refused) > 0 {
		parts := make([]string, 0, len(refused))
		for _, r := range refused {
			parts = append(parts, r.Name+" ("+r.Reason+")")
		}
		lines = append(lines, fmt.Sprintf("Refused by the validator, so not loaded (%d; edit within it or forget): %s", len(refused), strings.Join(parts, "; ")))
	}
	return lines
}

// ProjectRoot is the directory a project-scoped memory is filed under: the
// nearest ancestor of cwd holding .git, else cwd itself, with symlinks
// resolved, so a session started in a subdirectory sees the project's
// memories.
func ProjectRoot(cwd string) string {
	if cwd == "" {
		return ""
	}
	dir := filepath.Clean(cwd)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}
