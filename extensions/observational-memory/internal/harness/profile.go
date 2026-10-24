// Package harness is the per-model harness (docs/memory-interface.md, section 6): each model
// id gets a profile (the codec it reads and writes, the summary layout and size, which tools
// the agent defined stay active for it, and how long a format spec it is given) computed as a
// pure function of the model's usage ledger in the curated store. The package also owns the
// tools the agent defines with the memory tool: their probation, registration and per-model
// activation.
//
// The harness never changes the base memory tool or the room budget: a profile can pick a
// codec, a layout and a smaller summary within the room, and deactivate defined tools, but the
// memory tool stays registered and active and the summary is never larger than the room.
package harness

import (
	"fmt"
	"math/bits"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// MinEvidence is the minimum-evidence rule every switch follows: a setting switches only when
// at least MinEvidence events of the kind it reacts to were counted since it last switched,
// and they outnumber the events that speak for keeping it. One event is chance and two can be
// one mistake and its retry; three is the smallest count that is a repeat after a retry. It is
// a rule about how much evidence a switch needs, not a score tuned on a benchmark. The rules
// the harness writes (internal/rules) follow the same rule over passes and models.
const MinEvidence = rules.MinEvidence

// maxSummaryWindow is the largest context window the summary rule plans for (2M tokens).
const maxSummaryWindow = 2_000_000

// MaxSummaryHalvings bounds how often the summary size halves: once the room is divided by
// 2^MaxSummaryHalvings, any room up to a 2M-token window is below the summary floor
// (config.CompactionSummaryFallbackMaxTokens), which governs instead, so a further halving
// could change nothing.
var MaxSummaryHalvings = bits.Len(uint(maxSummaryWindow / config.CompactionSummaryFallbackMaxTokens))

// The settings a profile switches.
const (
	SettingCodec   = "codec"
	SettingLayout  = "layout"
	SettingSummary = "summary"
	SettingSpec    = "instructions"
	SettingTool    = "tool"
)

// Switch is one change the profile made, with the evidence that made it.
type Switch struct {
	Setting string
	// To is the new value: a codec name, a layout, a summary scale, "long spec", or for a tool
	// "<name> off".
	To     string
	Reason string
	// At is when the deciding event was recorded.
	At time.Time
}

// String is the switch as /om:status lists it.
func (s Switch) String() string {
	at := ""
	if !s.At.IsZero() {
		at = s.At.UTC().Format("2006-01-02 15:04") + " "
	}
	return fmt.Sprintf("%s%s → %s: %s", at, s.Setting, s.To, s.Reason)
}

// Profile is a model's harness settings.
type Profile struct {
	// Model is the model id (provider/id); "" for the default profile of a host without a store.
	Model string
	// Codec names the codec the model reads and writes tables in.
	Codec string
	// Layout is the summary layout: ledger.FormatTables or ledger.FormatLines.
	Layout string
	// SummaryHalvings is how often the summary size was halved: the summary gets the room
	// divided by 2^SummaryHalvings, never less than the summary floor, never more than the
	// room (ApplySummary).
	SummaryHalvings int
	// LongSpec carries the codec's longer spec with examples instead of its short one.
	LongSpec bool
	// DisabledTools are defined tools deactivated for this model.
	DisabledTools []string
	// Switches are the changes from the default, in the order they were made.
	Switches []Switch
	// Events is how many ledger events the profile was computed from.
	Events int
	// codec is the codec Codec names in the model's layer (declare.Active.Codec), when the
	// profile was computed with one; nil looks the name up among the shipped codecs.
	codec tables.Codec
}

// Default is the profile every model starts with: the first codec, the tables layout, the
// whole room, the short spec, every defined tool active.
func Default(model string) Profile {
	return Profile{Model: model, Codec: tables.Codecs()[0].Name(), Layout: ledger.FormatTables, DisabledTools: []string{}, Switches: []Switch{}}
}

// CodecValue is the profile's codec: the codec its name stands for in the model's layer (a
// format the model declared, else the shipped codec), the default codec when the name is
// unknown.
func (p Profile) CodecValue() tables.Codec {
	if p.codec != nil && p.codec.Name() == p.Codec {
		return p.codec
	}
	if c, ok := tables.Lookup(p.Codec); ok {
		return c
	}
	return tables.Codecs()[0]
}

// Spec is the format spec the model is given: the codec's long spec when LongSpec is set.
func (p Profile) Spec() string {
	if p.LongSpec {
		return tables.LongSpec(p.CodecValue())
	}
	return p.CodecValue().Spec()
}

// SummaryScale is the share of the room the summary gets, 1 for the whole room.
func (p Profile) SummaryScale() float64 {
	return 1 / float64(int(1)<<p.halvings())
}

func (p Profile) halvings() int { return max(0, min(p.SummaryHalvings, MaxSummaryHalvings)) }

// ApplySummary is the summary budget for this model within the room budget: the room divided
// by 2^SummaryHalvings, raised to the summary floor (config.CompactionSummaryFallbackMaxTokens)
// and never above the room itself, so no profile can grow the summary past the room.
func (p Profile) ApplySummary(room int) int {
	if room <= 0 {
		return room
	}
	scaled := room >> p.halvings()
	return min(room, max(scaled, min(room, config.CompactionSummaryFallbackMaxTokens), 1))
}

// SummaryFormat is the summary layout to render with: lines when the profile switched to it
// or the configuration asks for it, else tables.
func (p Profile) SummaryFormat(cfg config.Config) string {
	if p.Layout == ledger.FormatLines || cfg.MemoryFormat == config.MemoryFormatLines {
		return ledger.FormatLines
	}
	return ledger.FormatTables
}

// ToolDisabled reports whether the defined tool name is deactivated for this model.
func (p Profile) ToolDisabled(name string) bool {
	for _, n := range p.DisabledTools {
		if n == name {
			return true
		}
	}
	return false
}

// tally counts the events for and against a switch since the setting last switched.
type tally struct{ against, keep int }

// due reports whether the events against the setting meet the minimum-evidence rule and
// outnumber those that keep it.
func (t tally) due() bool { return t.against >= MinEvidence && t.against > t.keep }

func (t tally) reason(against, keep string) string {
	return fmt.Sprintf("%d %s vs %d %s since the last switch (needs at least %d and a majority)", t.against, against, t.keep, keep, MinEvidence)
}

// Compute is a model's profile from its usage ledger, read in the order it was written, and
// the store's definitions. It is pure: the same events give the same profile. The rules:
//
//   - codec: rows decodes in the current codec; it switches to the next codec (tables.Codecs
//     order) when decode errors meet the minimum-evidence rule against parsed decodes. The
//     last codec is kept: there is no further one to try, and cycling back would oscillate.
//   - layout: rows decodes that parsed with the wrong columns against those that parsed with
//     the right ones; tables switches to lines, once.
//   - instructions: syntax errors (memory queries that did not parse, rows the codec could
//     not decode) against syntactically valid queries and rows; the long spec is carried from
//     then on, once.
//   - summary: memory tool hits on records the current summary already showed against hits
//     on records it omitted. Shown hits halve the summary (it was not used); omitted hits
//     double it back toward the whole room. Each switch resets both counts.
//   - tools: a defined tool's calls by this model since it was (re)defined (calls before its
//     definition's creation time are of an earlier definition of the name and do not count);
//     it is deactivated for this model when errors meet the minimum-evidence rule against
//     calls without error, and stays so until it is redefined.
func Compute(model string, events []curated.ModelEvent, definitions []curated.Definition) Profile {
	names := []string{}
	for _, c := range tables.Codecs() {
		names = append(names, c.Name())
	}
	return ComputeWith(model, events, definitions, names)
}

// ComputeWith is Compute switching codecs through codecs, in order: the codec names of the
// model's layer (declare.Active.CodecNames: the shipped codecs, then the formats the model
// declared under new names). The first is the default.
func ComputeWith(model string, events []curated.ModelEvent, definitions []curated.Definition, codecs []string) Profile {
	p := Default(model)
	p.Events = len(events)
	if len(codecs) == 0 {
		codecs = []string{p.Codec}
	}
	p.Codec = codecs[0]
	codecIndex := 0
	var codec, layout, spec, summary tally
	created := map[string]time.Time{}
	for _, d := range definitions {
		created[d.Name] = d.Created
	}
	toolTally := map[string]*tally{}
	disabled := map[string]bool{}
	record := func(setting, to, reason string, at time.Time) {
		p.Switches = append(p.Switches, Switch{Setting: setting, To: to, Reason: reason, At: at})
	}
	for _, e := range events {
		at, _ := time.Parse(time.RFC3339, e.Time)
		switch e.Kind {
		case curated.ModelKindDecode:
			if e.Outcome == curated.OutcomeError {
				spec.against++
			} else {
				spec.keep++
			}
			if e.Codec == codecs[codecIndex] && codecIndex < len(codecs)-1 {
				if e.Outcome == curated.OutcomeError {
					codec.against++
				} else {
					codec.keep++
				}
				if codec.due() {
					codecIndex++
					record(SettingCodec, codecs[codecIndex], codec.reason("rows decode errors", "decodes")+" in "+e.Codec, at)
					codec = tally{}
				}
			}
			if p.Layout == ledger.FormatTables {
				switch e.Outcome {
				case curated.OutcomeColumns:
					layout.against++
				case curated.OutcomeOK:
					layout.keep++
				}
				if layout.due() {
					p.Layout = ledger.FormatLines
					record(SettingLayout, ledger.FormatLines, layout.reason("rows with wrong columns", "rows with the right columns"), at)
				}
			}
		case curated.ModelKindMemory:
			if e.Syntax {
				spec.against++
			} else {
				spec.keep++
			}
			if e.Target == curated.TargetSummary {
				switch e.Outcome {
				case curated.OutcomeShown:
					summary.against++
				case curated.OutcomeOmitted:
					summary.keep++
				}
				switch {
				case summary.due() && p.SummaryHalvings < MaxSummaryHalvings:
					p.SummaryHalvings++
					record(SettingSummary, scaleText(p), summary.reason("queries for records the summary showed", "for records it omitted"), at)
					summary = tally{}
				case p.SummaryHalvings > 0 && summary.keep >= MinEvidence && summary.keep > summary.against:
					p.SummaryHalvings--
					record(SettingSummary, scaleText(p), fmt.Sprintf("%d queries for records the summary omitted vs %d for records it showed since the last switch (needs at least %d and a majority)", summary.keep, summary.against, MinEvidence), at)
					summary = tally{}
				}
			}
		case curated.ModelKindDefined:
			if e.Tool == "" || disabled[e.Tool] {
				break
			}
			if c, ok := created[e.Tool]; ok && at.Before(c) {
				break
			}
			t := toolTally[e.Tool]
			if t == nil {
				t = &tally{}
				toolTally[e.Tool] = t
			}
			if e.Outcome == curated.OutcomeError {
				t.against++
			} else {
				t.keep++
			}
			if t.due() {
				disabled[e.Tool] = true
				p.DisabledTools = append(p.DisabledTools, e.Tool)
				record(SettingTool, e.Tool+" off", t.reason("errors", "calls without error"), at)
			}
		}
		if !p.LongSpec && spec.due() {
			p.LongSpec = true
			record(SettingSpec, "long spec", spec.reason("syntax errors", "valid queries and rows"), at)
		}
	}
	p.Codec = codecs[codecIndex]
	return p
}

// ApplyRules applies the switch-codec and long-spec rules that apply to the model
// (docs/memory-interface.md, section 7) to a computed profile: switch-codec makes the codec
// switch section 6's decode evidence would make (the next codec, unless the profile already
// left the first), long-spec carries the long spec. Each is recorded as a switch naming the
// rule. No rule touches the memory tool or the room.
func ApplyRules(p Profile, active rules.Active) Profile {
	codecs := tables.Codecs()
	for _, r := range active.Rules {
		switch r.Remedy {
		case rules.RemedySwitchCodec:
			if p.Codec == codecs[0].Name() && len(codecs) > 1 {
				p.Codec = codecs[1].Name()
				p.Switches = append(p.Switches, Switch{Setting: SettingCodec, To: p.Codec, Reason: "rule " + r.ID + " (" + r.State + ")", At: r.Created})
			}
		case rules.RemedyLongSpec:
			if !p.LongSpec {
				p.LongSpec = true
				p.Switches = append(p.Switches, Switch{Setting: SettingSpec, To: "long spec", Reason: "rule " + r.ID + " (" + r.State + ")", At: r.Created})
			}
		}
	}
	return p
}

func scaleText(p Profile) string {
	if p.SummaryHalvings == 0 {
		return "the whole room"
	}
	return fmt.Sprintf("1/%d of the room", 1<<p.SummaryHalvings)
}
