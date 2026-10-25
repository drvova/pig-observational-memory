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

// projectSettingsLine says why the project's settings file was not read, ""
// when it was read or there is none.
func projectSettingsLine(cfg config.Config) string {
	if !cfg.ProjectSettingsIgnored {
		return ""
	}
	if cfg.ProjectTrust == config.ProjectTrustUnknown {
		return "Project settings ignored: the project trust check failed, so only global settings apply"
	}
	return "Project settings ignored: the project is not trusted, so only global settings apply (a /trust decision applies after PiG restarts)"
}

// compactionLine is the status line on the next compaction. Under memory v2 it is PiG's own
// threshold (window - reserveTokens) and the reported context against it: PiG compacts with its
// auto-compaction on; with it off only the extension's level-0 compaction runs, at that
// threshold. Under v1 it is the proactive trigger's progress.
func compactionLine(cfg config.Config, h host.Host, progress, threshold int) string {
	if !cfg.V2() {
		return fmt.Sprintf("Next compaction:  ~%s / %s %s source tokens (%d%%)", jsfmt.Locale(progress), jsfmt.Locale(threshold), hooks.ProgressLabel(cfg), pct(progress, threshold))
	}
	pig := hooks.ReadV2Threshold(cfg, h)
	against := "PiG's threshold unknown (no context window)"
	if pig.ContextWindow > 0 {
		context := "context unknown until the next response reports usage"
		if pig.Tokens != nil {
			context = fmt.Sprintf("context ~%s (%d%%)", jsfmt.Locale(*pig.Tokens), pct(*pig.Tokens, pig.Limit()))
		}
		against = fmt.Sprintf("PiG's threshold %s (window %s - reserveTokens %s), %s", jsfmt.Locale(pig.Limit()), jsfmt.Locale(pig.ContextWindow), jsfmt.Locale(pig.ReserveTokens), context)
	}
	if cfg.PiAutoCompactionEnabled {
		return "Compaction: PiG's auto-compaction is on; " + against
	}
	return "Compaction: PiG's auto-compaction is off; only the extension's level-0 compaction runs, at " + against
}

// lastCompactionLine is memory v2's status line on the latest compaction: its level, whether
// level 1 made its request, what it recorded, and why it did not run or failed; "" under v1,
// before any compaction, and for a compaction memory v2 did not write.
func lastCompactionLine(cfg config.Config, entries []ledger.Entry) string {
	if !cfg.V2() {
		return ""
	}
	index := ledger.FindLastCompactionIndex(entries)
	if index < 0 {
		return ""
	}
	details, ok := ledger.MemoryDetailsFrom(entries[index].Details)
	if !ok || details.Engine != config.EngineV2 || details.Consolidation == nil {
		return "Last compaction: not written by memory v2"
	}
	report := details.Consolidation
	requests := 0
	if report.Requested {
		requests = 1
	}
	line := fmt.Sprintf("Last compaction: level %d, %d request", report.Level(), requests)
	if requests != 1 {
		line += "s"
	}
	switch {
	case report.Applied:
		line += fmt.Sprintf("; level 1 recorded %d labelled records, %d merges, %d reflections, %d aliases, dropped %d replaced records", report.Records, report.Merges, report.Reflections, report.Aliases, report.Dropped)
		if report.Confirmed+report.Rejected > 0 {
			line += fmt.Sprintf(", settled %d possible updates (%d confirmed, %d rejected)", report.Confirmed+report.Rejected, report.Confirmed, report.Rejected)
		}
		if report.Cited > 0 {
			line += fmt.Sprintf(", cited %d curated memories (%d staged made live)", report.Cited, report.Promoted)
		}
		if report.Refused > 0 {
			line += fmt.Sprintf(", refused %d proposals", report.Refused)
		}
	case report.Requested:
		line += "; level 1 failed (" + report.Reason + "), level 0 stands"
	default:
		line += "; level 1 not run: " + report.Reason
	}
	return line
}

// StatusLines renders the /om:status report.
func StatusLines(rt *runtime.Runtime, h host.Host) ([]string, error) {
	rt.EnsureConfig(h)
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
	// One memory-tool list for every observer figure below.
	memoryTools := harness.MemoryToolNames(rt)
	obsProgress := hooks.ObserverProgressTokens(entries, h, memoryTools)
	reflectionProgress := hooks.ReflectorProgressTokens(entries, h)
	compactionProgress := hooks.CompactionProgress(cfg, entries)
	// What the observer still has to read before the compaction cut can pass
	// it: entries that render nothing for it (memory-tool calls and results
	// among them) never need coverage.
	unobservedSinceCompaction := ledger.RawTokensAfterIndexWhere(entries, max(ledger.LatestCoverageIndex(entries, ledger.OMObservationsRecorded), ledger.CompactionRangeStartIndex(entries)-1), serialize.ObserverRendersFunc(memoryTools))
	compactThreshold := config.ResolveCompactAfterTokens(cfg, host.ModelContextWindow(h.SessionModel()))

	lines := []string{}
	if cfg.Passive {
		lines = append(lines,
			"── Mode ──",
			"Passive: automatic memory workers and auto-compaction disabled; manual/Pi compaction, commands, and recall remain active",
			"",
		)
	}
	if line := projectSettingsLine(cfg); line != "" {
		lines = append(lines, "── Settings ──", line, "")
	}
	lines = append(lines,
		"── Memory ──",
		observationLine,
		reflectionLine,
		"",
		"── Activity ──",
		fmt.Sprintf("Next observation: ~%s / %s tokens (%d%%)", jsfmt.Locale(obsProgress), jsfmt.Locale(cfg.ObserveAfterTokens), pct(obsProgress, cfg.ObserveAfterTokens)),
		fmt.Sprintf("Next reflection:  ~%s / %s observed tokens (%d%%)", jsfmt.Locale(reflectionProgress), jsfmt.Locale(cfg.ReflectAfterTokens), pct(reflectionProgress, cfg.ReflectAfterTokens)),
		compactionLine(cfg, h, compactionProgress, compactThreshold),
	)
	if line := lastCompactionLine(cfg, entries); line != "" {
		lines = append(lines, line)
	}
	lines = append(lines,
		fmt.Sprintf("Observer backlog: ~%s unobserved source tokens since the compaction boundary", jsfmt.Locale(unobservedSinceCompaction)),
		fmt.Sprintf("Visible observation pool: ~%s / %s tokens (%d%%)", jsfmt.Locale(visibleObservationTokens), jsfmt.Locale(cfg.ObservationsPoolMaxTokens), pct(visibleObservationTokens, cfg.ObservationsPoolMaxTokens)),
		fmt.Sprintf("Active observation pool: ~%s / %s target tokens (%d%%)", jsfmt.Locale(activePool.ObservationTokens), jsfmt.Locale(cfg.ObservationsPoolTargetTokens), pct(activePool.ObservationTokens, cfg.ObservationsPoolTargetTokens)),
		fmt.Sprintf("Reflection pool:         ~%s tokens", jsfmt.Locale(visibleReflectionTokens)),
	)
	// The observer backoff is measured on the observer's backlog alone, not on
	// the larger figure "Next observation" shows.
	if backoff := hooks.ActiveObserverBackoff(rt, h, entries, cfg, memoryTools); backoff != nil {
		lines = append(lines, fmt.Sprintf("Observer backoff: nothing new at ~%s backlog tokens; resumes at ~%s (backlog now ~%s)",
			jsfmt.Locale(backoff.TokensAtEmpty), jsfmt.Locale(backoff.TokensAtEmpty+cfg.ObserveAfterTokens), jsfmt.Locale(hooks.ObserverBacklog(entries, memoryTools))))
	}
	if suspension := hooks.ActiveV2CompactionSuspension(rt, h, entries); cfg.V2() && suspension != nil {
		lines = append(lines, fmt.Sprintf("Compaction trigger suspended: %s; asks again once the branch grows", suspension.Reason))
	}
	if backoff := hooks.ActiveCompactionBackoff(rt, h, entries); !cfg.V2() && backoff != nil {
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
	if curatedLines := harness.CuratedStatusLines(h, cfg); len(curatedLines) > 0 {
		lines = append(append(lines, ""), curatedLines...)
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
