package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
)

// The build and revision passes (docs/memory-interface.md, section 8): the memory model is
// given the kernel's rules, the seeds and the definition kinds (a revision also gets its counted
// evidence and its current layer) and answers with harness definitions through one tool. The
// worker only collects what passes Check; the caller holds each definition once the run is
// over and its context is still live, so an aborted pass leaves nothing behind.

// BuilderDefinition is one harness definition the builder proposes, or one it is shown.
type BuilderDefinition struct {
	Kind, Name, Text string
	// State is a shown definition's state (seed, held, written); empty for a proposal.
	State string
}

// BuilderArgs are one build or revision pass's inputs.
type BuilderArgs struct {
	WorkerArgs
	// ModelKey is the model whose layer is built (provider/id).
	ModelKey string
	// Revision marks a revision pass: Evidence and Current are set.
	Revision bool
	// KernelNames lists the kernel-owned names, one per line with their owners.
	KernelNames string
	// Seeds are the shipped definitions.
	Seeds []BuilderDefinition
	// Current is the model's effective layer (revision only).
	Current []BuilderDefinition
	// Evidence is the model's counted evidence (revision only), as rules.Evidence.Text.
	Evidence string
	// Check validates one proposal (the layer's checks and the provenance check); a refusal is
	// handed back to the model so it can fix the definition.
	Check func(BuilderDefinition) error
}

// BuilderResult is what a build or revision pass proposed and Check accepted, in order, with
// one entry per name (a later proposal of a name replaces the earlier).
type BuilderResult struct {
	Definitions []BuilderDefinition
	// Refused counts the proposals Check refused.
	Refused int
}

// BuilderKinds describes the definition kinds as the builder's prompt carries them.
const BuilderKinds = `Definition kinds (kind: what you define; how the kernel uses it):
- format: a table format declared as data (header form, delimiter, quote, escape, the characters that force quoting, the indent); a generic strict codec interprets it, and it must round-trip the kernel's test tables before it is accepted.
- schema: the fields your notes carry beyond the kernel's (id, timestamp, content, sources, relevance); validated and stored with each note.
- instructions: your own observer, reflector or dropper instructions (names: observer, reflector, dropper); appended after the kernel's fixed rules, never in place of them.
- summary: the memory query, or a declaration of up to eight sections, that selects and groups what the compaction summary shows; run under the room budget, and the kernel still discloses every omitted record.
- detector: a kernel check or a memory query over your notes and the history a pass covered that counts a failure; it flags problems and never judges a remedy.
- remedy: a catalogue named after a catalogue remedy (prompt-line, require-kind, keep-head, block-link, protect-kind, split-chunk, switch-codec, long-spec): the detectors it answers, the order the harness tries it, and its parameters. Rules are proposed with the memory tool's propose, not defined here.
Defined tools are made with the memory tool's define, not here.`

var defineHarnessSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"definitions": map[string]any{
			"type":     "array",
			"minItems": 1,
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"kind": map[string]any{"type": "string", "enum": []any{"format", "schema", "instructions", "summary", "detector", "remedy"}},
					"name": map[string]any{"type": "string", "minLength": 1},
					"text": map[string]any{"type": "string", "minLength": 1},
				},
				"required": []any{"kind", "name", "text"},
			},
		},
	},
	"required": []any{"definitions"},
}

// formatDefinitions lists definitions for the prompt, each a header line and its text.
func formatDefinitions(defs []BuilderDefinition) string {
	if len(defs) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for i, d := range defs {
		if i > 0 {
			b.WriteString("\n\n")
		}
		header := d.Kind + "/" + d.Name
		if d.State != "" {
			header += " (" + d.State + ")"
		}
		b.WriteString("### " + header + "\n" + strings.TrimRight(d.Text, "\n"))
	}
	return b.String()
}

// RunBuilder runs one build or revision pass. It returns the accepted proposals; none when the
// model defined nothing (a valid answer: the seeds stand), and a *StreamError when the model
// call failed before anything was accepted.
func RunBuilder(ctx context.Context, args BuilderArgs) (BuilderResult, error) {
	stage := "builder"
	if args.Revision {
		stage = "reviser"
	}
	accepted := map[string]int{}
	result := BuilderResult{}
	defineHarness := Tool{
		Name:        "define_harness",
		Label:       "Define harness",
		Description: "Define or change definitions of your memory harness. Each is checked by the kernel and held until a clean pass proves it.",
		Parameters:  defineHarnessSchema,
		Execute: func(_ context.Context, params map[string]any) (ToolResult, error) {
			items, _ := params["definitions"].([]any)
			lines := []string{}
			for _, raw := range items {
				item, _ := raw.(map[string]any)
				kind, _ := item["kind"].(string)
				name, _ := item["name"].(string)
				text, _ := item["text"].(string)
				d := BuilderDefinition{Kind: strings.TrimSpace(kind), Name: strings.TrimSpace(name), Text: strings.TrimRight(text, "\n")}
				var err error
				if args.Check != nil {
					err = args.Check(d)
				}
				if err != nil {
					result.Refused++
					lines = append(lines, fmt.Sprintf("%s/%s refused: %v", d.Kind, d.Name, err))
					continue
				}
				key := d.Kind + "/" + d.Name
				if i, ok := accepted[key]; ok {
					result.Definitions[i] = d
				} else {
					accepted[key] = len(result.Definitions)
					result.Definitions = append(result.Definitions, d)
				}
				lines = append(lines, key+" accepted; it is held once this pass ends")
			}
			if len(lines) == 0 {
				lines = append(lines, "no definitions given")
			}
			return ToolResult{Text: strings.Join(lines, "\n"), Details: map[string]any{"accepted": len(result.Definitions), "refused": result.Refused}}, nil
		},
	}
	var user strings.Builder
	fmt.Fprintf(&user, "MODEL: %s\n\nKERNEL-OWNED NAMES (never define these):\n%s\n\n%s\n\nSEED DEFINITIONS (what stands where you define nothing):\n%s\n", args.ModelKey, args.KernelNames, BuilderKinds, formatDefinitions(args.Seeds))
	if args.Revision {
		fmt.Fprintf(&user, "\nYOUR CURRENT LAYER:\n%s\n\nYOUR COUNTED EVIDENCE:\n%s\nRevise your harness where the evidence shows a failure your definitions can address. If nothing should change, do not call the tool.", formatDefinitions(args.Current), args.Evidence)
	} else {
		user.WriteString("\nBuild your starting harness: define what you would do differently from the seeds, in your own words. If the seeds suit you, do not call the tool.")
	}
	debuglog.Log(ctx, stage+".agent_start", map[string]any{"model": args.ModelKey, "seeds": len(args.Seeds), "current": len(args.Current)})
	loop, err := RunLoop(ctx, args.Stream, args.loopConfig(stage), Compose(kernel.TextBuilderRules, ""), user.String(), []Tool{defineHarness})
	if err != nil {
		return BuilderResult{}, err
	}
	if len(result.Definitions) == 0 && loop.StreamError != nil {
		return BuilderResult{}, loop.StreamError
	}
	debuglog.Log(ctx, stage+".result", map[string]any{"accepted": len(result.Definitions), "refused": result.Refused, "turns": loop.Turns})
	return result, nil
}
