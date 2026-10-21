package commands

import (
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/clipboard"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
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

// ViewUsage is the /om:view usage line.
const ViewUsage = "Usage: /om:view [full]"

// ViewOutput renders /om:view for a mode ("", "visible" or "full"); usage is
// true when the mode is unknown.
func ViewOutput(entries []ledger.Entry, mode string) (output string, usage bool) {
	switch mode {
	case "full":
		return RenderContentOnlyProjection(ledger.FullProjection(entries, ""), "recorded"), false
	case "", "visible":
		return RenderContentOnlyProjection(ledger.VisibleProjection(entries, ""), "visible"), false
	}
	return ViewUsage, true
}

// CopyFunc copies text to the clipboard and reports success.
type CopyFunc func(text string) bool

// DefaultCopy uses the platform clipboard commands.
func DefaultCopy(text string) bool { return clipboard.Copy(text, nil, nil) }

// RunView is the /om:view handler: print the memory and copy it to the clipboard.
func RunView(rt *runtime.Runtime, h host.Host, args string, copyText CopyFunc) error {
	rt.EnsureConfig(h.Cwd())
	if copyText == nil {
		copyText = DefaultCopy
	}
	entries, err := h.GetBranch()
	if err != nil {
		return err
	}
	output, usage := ViewOutput(entries, FirstArg(args))
	if usage {
		h.Notify(output, "info")
		return nil
	}
	copied := false
	func() {
		defer func() { _ = recover() }()
		copied = copyText(output)
	}()
	if copied {
		h.Notify(output+"\n\nCopied /om:view output to clipboard.", "info")
	} else {
		h.Notify(output+"\n\nWarning: failed to copy /om:view output to clipboard.", "info")
	}
	return nil
}
