// Package commands implements the /om:status and /om:view slash commands.
package commands

import (
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/hooks"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
)

func pct(current, total int) int {
	if total <= 0 {
		return 0
	}
	return int(float64(current)/float64(total)*100 + 0.5)
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

// compactionLine is the status line on the next compaction: PiG's own threshold (window -
// reserveTokens) and the reported context against it. PiG compacts with its auto-compaction
// on; with it off only the extension's level-0 compaction runs, at that threshold.
func compactionLine(cfg config.Config, h host.Host) string {
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

// lastCompactionLine is the status line on the latest compaction: its level, whether level 1
// made its request, what it recorded, and why it did not run or failed; "" before any
// compaction. A compaction memory v2 did not write (PiG's own summary, or memory v1's) says so.
func lastCompactionLine(entries []ledger.Entry) string {
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
		if report.HarnessOffered {
			line += fmt.Sprintf(", held %d harness definitions", report.Defined)
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

// removedSettingsLine lists the removed settings that are still set (config.RemovedSettings);
// "" when none is.
func removedSettingsLine(cfg config.Config) string {
	if len(cfg.RemovedSet) == 0 {
		return ""
	}
	return "Removed settings, ignored: " + strings.Join(cfg.RemovedSet, ", ")
}

// recordsLines are memory v2's memory lines: the records by who appended them (level 1's
// batches are the model's, whatever origin a label kept; otherwise code, the host summaries
// the ledger did not back, and v1's workers by origin), dropped and active, and the
// reflections standing (ledger.ActiveReflections).
func recordsLines(entries []ledger.Entry) []string {
	folded := ledger.FoldLedger(entries, "")
	byLevel1 := ledger.ConsolidatedObservationIDs(entries)
	by := map[string]int{}
	for _, o := range folded.Observations {
		switch {
		case byLevel1[o.ID] || o.Origin == ledger.OriginModel:
			by[ledger.OriginModel]++
		default:
			by[o.Origin]++
		}
	}
	line := fmt.Sprintf("Records: %d recorded (%d by code, %d by the model", len(folded.Observations), by[ledger.OriginCode], by[ledger.OriginModel])
	if n := by[ledger.OriginHost]; n > 0 {
		noun := "summaries"
		if n == 1 {
			noun = "summary"
		}
		line += fmt.Sprintf(", %d host %s", n, noun)
	}
	if n := by[""]; n > 0 {
		line += fmt.Sprintf(", %d by memory v1's workers", n)
	}
	line += fmt.Sprintf(") / %d dropped / %d active", len(folded.DroppedObservationIDs), len(folded.ActiveObservations))
	return []string{line, fmt.Sprintf("Reflections: %d recorded / %d standing", len(folded.Reflections), len(ledger.ActiveReflections(folded.Reflections)))}
}

// StatusFigures are the /om:status figures that do not depend on the session model
// (docs/code-tool.md, "Order of work"): the mode, the settings, the memory and the latest
// compaction, what is in flight, the curated memories, the harness lines that belong to no
// model, then one block per model with usage events, each computed from that model's own
// ledger and layer and labelled with it. It never reads the session model: om.Info answers
// with it, so the same store prints the same figures under any session model, and /om:status
// prints it before its session-model lines.
func StatusFigures(rt *runtime.Runtime, h host.Host, cfg config.Config, entries []ledger.Entry) om.StatusInfo {
	lines := []string{}
	if cfg.Passive {
		lines = append(lines, "── Mode ──", "Passive: turn-end extraction, level 1, thinning and the extension's compaction trigger are off; PiG's compactions, commands and recall remain active", "")
	}
	settings := []string{}
	for _, line := range []string{projectSettingsLine(cfg), removedSettingsLine(cfg)} {
		if line != "" {
			settings = append(settings, line)
		}
	}
	if len(settings) > 0 {
		lines = append(append(append(lines, "── Settings ──"), settings...), "")
	}
	lines = append(append(lines, "── Memory ──"), recordsLines(entries)...)
	if line := lastCompactionLine(entries); line != "" {
		lines = append(lines, line)
	}
	if state := rt.Snapshot(); state.CompactInFlight || state.CompactHookInFlight {
		lines = append(lines, "", "── In flight ──")
		if state.CompactInFlight {
			lines = append(lines, "Auto-compaction: running")
		}
		if state.CompactHookInFlight {
			lines = append(lines, "Compaction hook: running")
		}
	}
	if curatedLines := harness.CuratedStatusLines(h, cfg); len(curatedLines) > 0 {
		lines = append(append(lines, ""), curatedLines...)
	}
	if harnessLines := harness.SharedStatusLines(h, cfg); len(harnessLines) > 0 {
		lines = append(append(lines, ""), harnessLines...)
	}
	return om.StatusInfo{Lines: lines, Models: harness.UsageModelStatus(h, cfg)}
}

// statusLines is the /om:status report (docs/memory-v2.md, "Status"): the shared figures
// (StatusFigures: records, the last compaction's level, request count and level-1 failure
// reason, the curated memories, the harness, and one block per model with usage events), then
// the lines that depend on the session model: PiG's threshold and the context against it,
// whether auto-compaction is on, the trigger's suspension, and the session model's profile;
// last the memory tool's kept definitions and namespace child (runtime.Runtime.CodeStatus),
// which this process's state, not the store alone, decides, so om.Info leaves them out.
func statusLines(rt *runtime.Runtime, h host.Host, cfg config.Config, entries []ledger.Entry) []string {
	lines := strings.Split(StatusFigures(rt, h, cfg, entries).String(), "\n")
	lines = append(lines, "", "── Compaction ──", compactionLine(cfg, h))
	if suspension := hooks.ActiveV2CompactionSuspension(rt, h, entries); suspension != nil {
		lines = append(lines, fmt.Sprintf("Compaction trigger suspended: %s; asks again once the branch grows", suspension.Reason))
	}
	if session := harness.SessionStatusLines(h, cfg); len(session) > 0 {
		lines = append(append(lines, ""), session...)
	}
	if code := rt.CodeStatus(h); len(code) > 0 {
		lines = append(append(lines, "", "── Code ──"), code...)
	}
	return lines
}

// StatusLines renders the /om:status report.
func StatusLines(rt *runtime.Runtime, h host.Host) ([]string, error) {
	rt.EnsureConfig(h)
	cfg := rt.LiveConfig(h.Cwd())
	entries, err := h.GetBranch()
	if err != nil {
		return nil, err
	}
	return statusLines(rt, h, cfg, entries), nil
}

// Info is om.Info's answer (StatusFigures over h's branch and live configuration): the figures
// that belong to no model, then one block per model, never anything read from the session
// model. It starts, kills or replays no child.
func Info(rt *runtime.Runtime, h host.Host) (om.StatusInfo, error) {
	rt.EnsureConfig(h)
	cfg := rt.LiveConfig(h.Cwd())
	entries, err := h.GetBranch()
	if err != nil {
		return om.StatusInfo{}, err
	}
	return StatusFigures(rt, h, cfg, entries), nil
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
