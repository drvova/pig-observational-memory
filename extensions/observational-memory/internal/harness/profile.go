// Package harness is the per-model harness (docs/memory-interface.md, section 6): each model
// id gets a profile (the codec its compaction summary is written in, and the summary layout and
// size) computed as a pure function of the model's usage ledger in the curated store. The
// memory tool itself is the same for every model (docs/code-tool.md): no profile changes its
// description, schema or output, and no profile registers, activates or deactivates a tool.
// The package also runs the memory tool's calls on the namespace child. The tools the agent
// defined with the removed query language are retired (docs/code-tool.md, "Named deviation:
// agent-defined tools"): their definitions stay stored and are listed (RetiredQueries), never
// registered or run, and their names stay memory plumbing (Manager.SkipNames).
//
// The harness never changes the memory tool or the room budget: a profile can pick a codec, a
// layout and a smaller summary within the room, and the summary is never larger than the room.
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
)

// Switch is one change the profile made, with the evidence that made it.
type Switch struct {
	Setting string
	// To is the new value: a codec name, a layout or a summary scale.
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
	// Switches are the changes from the default, in the order they were made.
	Switches []Switch
	// Events is how many ledger events the profile was computed from.
	Events int
	// codec is the codec Codec names in the model's layer (declare.Active.Codec), when the
	// profile was computed with one; nil looks the name up among the shipped codecs.
	codec tables.Codec
}

// Default is the profile every model starts with: the first codec, the tables layout, the
// whole room.
func Default(model string) Profile {
	return Profile{Model: model, Codec: tables.Codecs()[0].Name(), Layout: ledger.FormatTables, Switches: []Switch{}}
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

// tally counts the events for and against a switch since the setting last switched.
type tally struct{ against, keep int }

// due reports whether the events against the setting meet the minimum-evidence rule and
// outnumber those that keep it.
func (t tally) due() bool { return t.against >= MinEvidence && t.against > t.keep }

func (t tally) reason(against, keep string) string {
	return fmt.Sprintf("%d %s vs %d %s since the last switch (needs at least %d and a majority)", t.against, against, t.keep, keep, MinEvidence)
}

// Compute is a model's profile from its usage ledger, read in the order it was written, with
// the shipped codecs (ComputeWith). It is pure: the same events
// give the same profile. The rules:
//
//   - codec: rows decodes in the current codec; it switches to the next codec (tables.Codecs
//     order) when decode errors meet the minimum-evidence rule against parsed decodes. The
//     last codec is kept: there is no further one to try, and cycling back would oscillate.
//   - layout: rows decodes that parsed with the wrong columns against those that parsed with
//     the right ones; tables switches to lines, once.
//   - summary: memory tool code calls' hits (ModelKindCode) on records the current summary
//     already showed against hits on records it omitted. Shown hits halve the summary (it was
//     not used); omitted hits double it back toward the whole room. Each switch resets both
//     counts. A code call's errors count toward nothing: no switch may change the tool by
//     model, so the long-spec instructions setting is retired, and the query language's
//     events (ModelKindMemory) stay as history and are never graded.
//
// No setting switches a tool: the defined tools are retired with the query language
// (docs/code-tool.md), so their calls' events (ModelKindDefined) stay as history and are never
// graded either.
func Compute(model string, events []curated.ModelEvent) Profile {
	names := []string{}
	for _, c := range tables.Codecs() {
		names = append(names, c.Name())
	}
	return ComputeWith(model, events, names)
}

// ComputeWith is Compute switching codecs through codecs, in order: the codec names of the
// model's layer (declare.Active.CodecNames: the shipped codecs, then the formats the model
// declared under new names). The first is the default.
//
// It reads memory v2's events alone (curated.ModelEvent.Engine): memory v1's worker evidence
// means nothing here, so v1's tallies never carry over. One evidence unit is one compaction
// (docs/memory-v2.md, "Self-built harness"): events are not applied one by one but gathered
// until the model's next compaction pass (rules.IsV2Compaction), where each setting gets at
// most one unit: one against it when its events against outnumbered those that keep it since
// the previous compaction, one for keeping it when the reverse, none on a tie. The
// minimum-evidence rule then asks for MinEvidence compactions, not MinEvidence calls of one
// turn. Events after the latest compaction wait for the next.
func ComputeWith(model string, events []curated.ModelEvent, codecs []string) Profile {
	own := make([]curated.ModelEvent, 0, len(events))
	for _, e := range events {
		if e.Engine == curated.EngineV2 {
			own = append(own, e)
		}
	}
	events = own
	p := Default(model)
	p.Events = len(events)
	if len(codecs) == 0 {
		codecs = []string{p.Codec}
	}
	p.Codec = codecs[0]
	c := &computation{p: &p, codecs: codecs}
	window := newVotes()
	for _, e := range events {
		at, _ := time.Parse(time.RFC3339, e.Time)
		if rules.IsV2Compaction(e) {
			c.apply(window.units(), at)
			window = newVotes()
			continue
		}
		c.classify(e, window)
	}
	p.Codec = codecs[c.codecIndex]
	return p
}

// computation is one ComputeWith's state: the profile being built, where it stands in the codec
// rotation, and the tallies since each setting last switched.
type computation struct {
	p                      *Profile
	codecs                 []string
	codecIndex             int
	codec, layout, summary tally
}

// votes are the events for and against each setting in one window: the events since the
// previous compaction. codecName is the codec the codec votes were
// cast in, the current codec when they were classified.
type votes struct {
	codec, layout, summary tally
	codecName              string
}

func newVotes() *votes { return &votes{} }

// unit is t as one evidence unit: one against when its events against outnumber those that
// keep, one keep when the reverse, none on a tie.
func (t tally) unit() tally {
	switch {
	case t.against > t.keep:
		return tally{against: 1}
	case t.keep > t.against:
		return tally{keep: 1}
	}
	return tally{}
}

// units are the window's votes as one evidence unit per setting (tally.unit).
func (v *votes) units() *votes {
	return &votes{codec: v.codec.unit(), layout: v.layout.unit(), summary: v.summary.unit(), codecName: v.codecName}
}

// classify adds e's votes to v, judged against the profile as it stands: a decode votes on the
// codec when it was in the current codec (and a later one exists), and on the layout while it
// is tables; a code call, for a hit checked against the summary, on its size. A query-language
// memory call (ModelKindMemory) and a retired defined tool's call (ModelKindDefined) vote on
// nothing.
func (c *computation) classify(e curated.ModelEvent, v *votes) {
	switch e.Kind {
	case curated.ModelKindDecode:
		if e.Codec == c.codecs[c.codecIndex] && c.codecIndex < len(c.codecs)-1 {
			v.codecName = e.Codec
			if e.Outcome == curated.OutcomeError {
				v.codec.against++
			} else {
				v.codec.keep++
			}
		}
		if c.p.Layout == ledger.FormatTables {
			switch e.Outcome {
			case curated.OutcomeColumns:
				v.layout.against++
			case curated.OutcomeOK:
				v.layout.keep++
			}
		}
	case curated.ModelKindCode:
		if e.Target == curated.TargetSummary {
			switch e.Outcome {
			case curated.OutcomeShown:
				v.summary.against++
			case curated.OutcomeOmitted:
				v.summary.keep++
			}
		}
	}
}

// apply adds a window's votes to the tallies and makes every switch they make due, in the
// order the switches have always been checked: codec, layout, then summary.
func (c *computation) apply(v *votes, at time.Time) {
	p := c.p
	record := func(setting, to, reason string) {
		p.Switches = append(p.Switches, Switch{Setting: setting, To: to, Reason: reason, At: at})
	}
	if v.codec != (tally{}) && v.codecName == c.codecs[c.codecIndex] && c.codecIndex < len(c.codecs)-1 {
		c.codec.against += v.codec.against
		c.codec.keep += v.codec.keep
		if c.codec.due() {
			c.codecIndex++
			record(SettingCodec, c.codecs[c.codecIndex], c.codec.reason("rows decode errors", "decodes")+" in "+v.codecName)
			c.codec = tally{}
		}
	}
	if p.Layout == ledger.FormatTables && v.layout != (tally{}) {
		c.layout.against += v.layout.against
		c.layout.keep += v.layout.keep
		if c.layout.due() {
			p.Layout = ledger.FormatLines
			record(SettingLayout, ledger.FormatLines, c.layout.reason("rows with wrong columns", "rows with the right columns"))
		}
	}
	if v.summary != (tally{}) {
		c.summary.against += v.summary.against
		c.summary.keep += v.summary.keep
		switch {
		case c.summary.due() && p.SummaryHalvings < MaxSummaryHalvings:
			p.SummaryHalvings++
			record(SettingSummary, scaleText(*p), c.summary.reason("queries for records the summary showed", "for records it omitted"))
			c.summary = tally{}
		case p.SummaryHalvings > 0 && c.summary.keep >= MinEvidence && c.summary.keep > c.summary.against:
			p.SummaryHalvings--
			record(SettingSummary, scaleText(*p), fmt.Sprintf("%d queries for records the summary omitted vs %d for records it showed since the last switch (needs at least %d and a majority)", c.summary.keep, c.summary.against, MinEvidence))
			c.summary = tally{}
		}
	}
}

// ApplyRules applies the switch-codec rules that apply to the model (docs/memory-interface.md,
// section 7) to a computed profile: switch-codec makes the codec switch section 6's decode
// evidence would make (the next codec, unless the profile already left the first), for the
// compaction summary only, recorded as a switch naming the rule. long-spec is retired with the
// query language (docs/code-tool.md): a rule naming it loads and is listed, never applied. No
// rule touches the memory tool or the room.
func ApplyRules(p Profile, active rules.Active) Profile {
	codecs := tables.Codecs()
	for _, r := range active.Rules {
		switch r.Remedy {
		case rules.RemedySwitchCodec:
			if p.Codec == codecs[0].Name() && len(codecs) > 1 {
				p.Codec = codecs[1].Name()
				p.Switches = append(p.Switches, Switch{Setting: SettingCodec, To: p.Codec, Reason: "rule " + r.ID + " (" + r.State + ")", At: r.Created})
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
