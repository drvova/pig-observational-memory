// Package commands implements the /om:status and /om:view slash commands.
package commands

import (
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/hooks"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

func pct(current, total int) int {
	if total <= 0 {
		return 0
	}
	return int(float64(current)/float64(total)*100 + 0.5)
}

func observationTokenSum(items []ledger.Observation) int {
	sum := 0
	for _, item := range items {
		sum += item.TokenCount
	}
	return sum
}

func reflectionTokenSum(items []ledger.Reflection) int {
	sum := 0
	for _, item := range items {
		sum += item.TokenCount
	}
	return sum
}

func appendSuffixes(line string, suffixes ...string) string {
	rendered := []string{}
	for _, suffix := range suffixes {
		if suffix != "" {
			rendered = append(rendered, suffix)
		}
	}
	if len(rendered) == 0 {
		return line
	}
	return line + " " + strings.Join(rendered, " ")
}

func addedSuffix(count int) string {
	if count > 0 {
		return "+" + jsfmt.Locale(count)
	}
	return ""
}

func removedSuffix(count int) string {
	if count > 0 {
		return "-" + jsfmt.Locale(count)
	}
	return ""
}

// StatusLines renders the /om:status report.
func StatusLines(rt *runtime.Runtime, h host.Host) ([]string, error) {
	rt.EnsureConfig(h.Cwd())
	cfg := rt.LiveConfig(h.Cwd())
	entries, err := h.GetBranch()
	if err != nil {
		return nil, err
	}
	folded := ledger.FoldLedger(entries, "")
	visible := ledger.VisibleProjection(entries, "")
	full := ledger.FullProjection(entries, "")
	drift := ledger.DiffProjection(visible, full)

	visibleObservationTokens := observationTokenSum(visible.Observations)
	visibleReflectionTokens := reflectionTokenSum(visible.Reflections)
	activePool := agents.ObservationPoolMetrics(folded.ActiveObservations, cfg.ObservationsPoolTargetTokens)
	observationLine := appendSuffixes(
		fmt.Sprintf("Observations: %d recorded / %d dropped / %d active / %d visible", len(folded.Observations), len(folded.DroppedObservationIDs), len(folded.ActiveObservations), len(visible.Observations)),
		addedSuffix(len(drift.ObservationsOnlyInFull)), removedSuffix(len(drift.DroppedOnlyInFull)),
	)
	reflectionLine := appendSuffixes(
		fmt.Sprintf("Reflections:  %d recorded / %d visible", len(folded.Reflections), len(visible.Reflections)),
		addedSuffix(len(drift.ReflectionsOnlyInFull)),
	)
	// The same measures that gate the workers: the larger of the raw backlog
	// and the provider-reported growth.
	obsProgress := hooks.ObserverProgressTokens(entries, h)
	reflectionProgress := hooks.ReflectorProgressTokens(entries, h)
	compactionProgress := hooks.CompactionProgress(cfg, entries)
	// What the observer still has to read before the compaction cut can pass
	// it: entries that render nothing for it never need coverage.
	unobservedSinceCompaction := ledger.RawTokensAfterIndexWhere(entries, max(ledger.LatestCoverageIndex(entries, ledger.OMObservationsRecorded), ledger.CompactionRangeStartIndex(entries)-1), serialize.ObserverRenders)
	compactThreshold := config.ResolveCompactAfterTokens(cfg, host.ModelContextWindow(h.SessionModel()))

	lines := []string{}
	if cfg.Passive {
		lines = append(lines,
			"── Mode ──",
			"Passive: automatic memory workers and auto-compaction disabled; manual/Pi compaction, commands, and recall remain active",
			"",
		)
	}
	lines = append(lines,
		"── Memory ──",
		observationLine,
		reflectionLine,
		"",
		"── Activity ──",
		fmt.Sprintf("Next observation: ~%s / %s tokens (%d%%)", jsfmt.Locale(obsProgress), jsfmt.Locale(cfg.ObserveAfterTokens), pct(obsProgress, cfg.ObserveAfterTokens)),
		fmt.Sprintf("Next reflection:  ~%s / %s observed tokens (%d%%)", jsfmt.Locale(reflectionProgress), jsfmt.Locale(cfg.ReflectAfterTokens), pct(reflectionProgress, cfg.ReflectAfterTokens)),
		fmt.Sprintf("Next compaction:  ~%s / %s %s source tokens (%d%%)", jsfmt.Locale(compactionProgress), jsfmt.Locale(compactThreshold), hooks.ProgressLabel(cfg), pct(compactionProgress, compactThreshold)),
		fmt.Sprintf("Observer backlog: ~%s unobserved source tokens since the compaction boundary", jsfmt.Locale(unobservedSinceCompaction)),
		fmt.Sprintf("Visible observation pool: ~%s / %s tokens (%d%%)", jsfmt.Locale(visibleObservationTokens), jsfmt.Locale(cfg.ObservationsPoolMaxTokens), pct(visibleObservationTokens, cfg.ObservationsPoolMaxTokens)),
		fmt.Sprintf("Active observation pool: ~%s / %s target tokens (%d%%)", jsfmt.Locale(activePool.ObservationTokens), jsfmt.Locale(cfg.ObservationsPoolTargetTokens), pct(activePool.ObservationTokens, cfg.ObservationsPoolTargetTokens)),
		fmt.Sprintf("Reflection pool:         ~%s tokens", jsfmt.Locale(visibleReflectionTokens)),
	)
	// The observer backoff is measured on the observer's backlog alone, not on
	// the larger figure "Next observation" shows.
	if backoff := hooks.ActiveObserverBackoff(rt, h, entries, cfg); backoff != nil {
		lines = append(lines, fmt.Sprintf("Observer backoff: nothing new at ~%s backlog tokens; resumes at ~%s (backlog now ~%s)",
			jsfmt.Locale(backoff.TokensAtEmpty), jsfmt.Locale(backoff.TokensAtEmpty+cfg.ObserveAfterTokens), jsfmt.Locale(hooks.ObserverBacklog(entries))))
	}
	if backoff := hooks.ActiveCompactionBackoff(rt, h, entries); backoff != nil {
		lines = append(lines, fmt.Sprintf("Compaction backoff: last attempt failed (%s); asks again at ~%s %s source tokens", backoff.Reason, jsfmt.Locale(backoff.Progress+compactThreshold), hooks.ProgressLabel(cfg)))
	}
	if hooks.ReflectorBackedOff(rt, h, entries) {
		lines = append(lines, "Reflector backoff: nothing stable in the current memory; resumes when it changes")
	}

	state := rt.Snapshot()
	if state.ConsolidationInFlight || state.CompactInFlight || state.CompactHookInFlight {
		lines = append(lines, "", "── In flight ──")
		if state.ConsolidationInFlight {
			phase := ""
			if state.ConsolidationPhase != "" {
				phase = " (" + string(state.ConsolidationPhase) + ")"
			}
			lines = append(lines, "Consolidation: running"+phase)
		}
		if state.CompactInFlight {
			lines = append(lines, "Auto-compaction: running")
		}
		if state.CompactHookInFlight {
			lines = append(lines, "Compaction hook: running")
		}
	}
	if state.LastObserverError != "" || state.LastReflectorError != "" || state.LastDropperError != "" {
		lines = append(lines, "", "── Last error ──")
		if state.LastObserverError != "" {
			lines = append(lines, "Observer: "+state.LastObserverError)
		}
		if state.LastReflectorError != "" {
			lines = append(lines, "Reflector: "+state.LastReflectorError)
		}
		if state.LastDropperError != "" {
			lines = append(lines, "Dropper: "+state.LastDropperError)
		}
	}
	if harnessLines := harness.StatusLines(h, cfg); len(harnessLines) > 0 {
		lines = append(append(lines, ""), harnessLines...)
	}
	return lines, nil
}

// RunStatus is the /om:status handler.
func RunStatus(rt *runtime.Runtime, h host.Host) error {
	lines, err := StatusLines(rt, h)
	if err != nil {
		return err
	}
	h.Notify(strings.Join(lines, "\n"), "info")
	return nil
}
