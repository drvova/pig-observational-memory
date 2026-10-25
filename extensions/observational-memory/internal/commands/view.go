package commands

import (
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/clipboard"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
)

// FirstArg is the command's first whitespace-separated argument.
func FirstArg(args string) string {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func renderReflections(items []ledger.Reflection, empty string) string {
	if len(items) == 0 {
		return empty
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, ledger.ReflectionToSummaryLine(item))
	}
	return strings.Join(lines, "\n")
}

func renderObservations(items []ledger.Observation, empty string) string {
	if len(items) == 0 {
		return empty
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, ledger.ObservationToSummaryLine(item))
	}
	return strings.Join(lines, "\n")
}

// RenderContentOnlyProjection renders reflections and observations for /om:view.
func RenderContentOnlyProjection(projection ledger.Projection, emptyScope string) string {
	return strings.Join([]string{
		"── Reflections ──",
		renderReflections(projection.Reflections, "No "+emptyScope+" reflections."),
		"",
		"── Observations ──",
		renderObservations(projection.Observations, "No "+emptyScope+" observations."),
	}, "\n")
}

// plural is count and noun, with an s for any count but one.
func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return jsfmt.Locale(count) + " " + noun + "s"
}

// NothingVisibleNote is what the default /om:view prints when the agent sees no
// memory although records exist, "" when it sees some or nothing is recorded:
// two empty sections would read as if the memory were empty.
func NothingVisibleNote(entries []ledger.Entry) string {
	visible := ledger.VisibleProjection(entries, "")
	if len(visible.Observations) > 0 || len(visible.Reflections) > 0 {
		return ""
	}
	full := ledger.FullProjection(entries, "")
	parts := []string{}
	if n := len(full.Observations); n > 0 {
		parts = append(parts, plural(n, "active observation"))
	}
	if n := len(full.Reflections); n > 0 {
		parts = append(parts, plural(n, "reflection"))
	}
	if len(parts) == 0 {
		return ""
	}
	verb, them := "are", "them"
	if len(full.Observations)+len(full.Reflections) == 1 {
		verb, them = "is", "it"
	}
	lead := "Nothing compacted yet"
	if ledger.FindLastCompactionIndex(entries) >= 0 {
		// Pi shows only the newest compaction, here one the native summarizer
		// (or an older version) wrote, or one that carried no record.
		lead = "The latest compaction carries no observational memory"
	}
	return fmt.Sprintf("%s: %s %s recorded; /om:view full shows %s.", lead, strings.Join(parts, " and "), verb, them)
}

// ViewUsage is the /om:view usage line.
const ViewUsage = "Usage: /om:view [full]"

// ViewOutput renders /om:view for a mode ("", "visible" or "full"); usage is true when the
// mode is unknown. It leaves out the reflections level 1 replaced (ledger.ActiveReflections),
// as the summaries and the memory tool do. It only reads.
func ViewOutput(entries []ledger.Entry, mode string) (output string, usage bool) {
	var projection ledger.Projection
	scope := "visible"
	switch mode {
	case "full":
		projection, scope = ledger.FullProjection(entries, ""), "recorded"
	case "", "visible":
		projection = ledger.VisibleProjection(entries, "")
	default:
		return ViewUsage, true
	}
	projection.Reflections = ledger.ActiveReflections(projection.Reflections)
	return RenderContentOnlyProjection(projection, scope), false
}

// CopyFunc copies text to the clipboard and reports what it did.
type CopyFunc func(text string) clipboard.Outcome

// DefaultCopy uses the platform clipboard commands that can reach a clipboard.
func DefaultCopy(text string) clipboard.Outcome { return clipboard.CopyIfAvailable(text) }

// Clipboard notices. A usable clipboard command that fails is a real failure
// and is reported every time; a session with no usable command (headless:
// none installed, or no display) is told once per process, since that does
// not change while the process runs.
const (
	viewCopied      = "Copied /om:view output to clipboard."
	viewCopyFailed  = "Warning: failed to copy /om:view output to clipboard."
	viewNoClipboard = "Note: no clipboard is available here, so /om:view output is not copied (shown once)."
)

// RunView is the /om:view handler: print the memory and copy it to the clipboard.
func RunView(rt *runtime.Runtime, h host.Host, args string, copyText CopyFunc) error {
	rt.EnsureConfig(h)
	if copyText == nil {
		copyText = DefaultCopy
	}
	entries, err := h.GetBranch()
	if err != nil {
		return err
	}
	mode := FirstArg(args)
	if mode == "" || mode == "visible" {
		// Nothing to copy: the note points at the records instead.
		if note := NothingVisibleNote(entries); note != "" {
			h.Notify(note, "info")
			return nil
		}
	}
	output, usage := ViewOutput(entries, mode)
	if usage {
		h.Notify(output, "info")
		return nil
	}
	outcome := clipboard.Failed
	func() {
		defer func() { _ = recover() }()
		outcome = copyText(output)
	}()
	switch {
	case outcome == clipboard.Copied:
		h.Notify(output+"\n\n"+viewCopied, "info")
	case outcome == clipboard.Unavailable && rt.ShowOnce("view-no-clipboard"):
		h.Notify(output+"\n\n"+viewNoClipboard, "info")
	case outcome == clipboard.Unavailable:
		h.Notify(output, "info")
	default:
		h.Notify(output+"\n\n"+viewCopyFailed, "info")
	}
	return nil
}
