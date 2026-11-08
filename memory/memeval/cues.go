package memeval

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/extract"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
)

// The cue rates (docs/brain-memory.md, decision 4 and phase 8; docs/memory-v2.md, "Cues") on the
// Tier 0 generator: each seed's session is replayed turn by turn with level 0 after every turn and
// a compaction boundary every CueCompactionEvery turns, and after each tool call the cue the
// post-tool hook would give is decided (extract.CueFor, cap extract.CueCap). Beside each call, a
// look-alike of it (its path with a letter added to its first component, its command with a flag added,
// each output line with its last digit changed) is decided over the same branch with no cue given
// yet: any cue it gets is a false cue (scenario 28). It is code only and makes no request.

// CueCompactionEvery is the replay's compaction cadence in turns (the Tier 0 runs compact about
// every 20 turns in the small window).
const CueCompactionEvery = 20

// CueTally is the replay's counts.
type CueTally struct {
	Seeds, Calls int
	// Eligible are calls a standing cueable record's key matches, cap aside; Cued the cues given
	// under the cap; Capped the eligible calls the cap or once-per-compaction withheld.
	Eligible, Cued, Capped int
	// LookAlikes are the look-alike calls decided, FalseCues the cues they got.
	LookAlikes, FalseCues int
	// Repeats are calls made at each session's end that repeat a standing cueable record's own
	// key exactly (its command, or a path it names, one inside a directory key), each decided with
	// no cue given yet; RepeatCued the cues they got (the cue's recall on exact repeats), and
	// RepeatLookAlikes and RepeatFalse the same for their look-alikes.
	Repeats, RepeatCued, RepeatLookAlikes, RepeatFalse int
}

// repeatCall is a call repeating o's first command or path key, false when it has neither.
func repeatCall(o ledger.Observation) (Call, bool) {
	if len(o.Keys.Commands) > 0 {
		return Call{Name: "bash", Args: map[string]any{"command": o.Keys.Commands[0]}}, true
	}
	if len(o.Keys.Paths) > 0 {
		p := o.Keys.Paths[0]
		if strings.HasSuffix(p, "/") {
			p += "notes.go"
		}
		return Call{Name: "edit", Args: map[string]any{"path": p}, Result: "ok"}, true
	}
	return Call{}, false
}

// lookAlike is call changed by one character in each key it carries, so no exact key matches.
func lookAlike(c Call) extract.CueCall {
	args := map[string]any{}
	for k, v := range c.Args {
		args[k] = v
	}
	for _, k := range []string{"path", "file_path", "filePath", "notebook_path"} {
		if p, ok := args[k].(string); ok && p != "" {
			// A letter added to the first path component, so neither the file nor a directory
			// holding it is the same.
			lead := len(p) - len(strings.TrimLeft(p, "/"))
			end := strings.Index(p[lead:], "/")
			if end < 0 {
				end = len(p) - lead
			}
			args[k] = p[:lead+end] + "x" + p[lead+end:]
		}
	}
	if cmd, ok := args["command"].(string); ok && cmd != "" {
		args["command"] = cmd + " -v"
	}
	var out []string
	for _, l := range strings.Split(c.Result, "\n") {
		b := []byte(l)
		for i := len(b) - 1; i >= 0; i-- {
			if b[i] >= '0' && b[i] <= '9' {
				b[i] = '0' + (b[i]-'0'+1)%10
				break
			}
		}
		if string(b) == l {
			b = append(b, '~')
		}
		out = append(out, string(b))
	}
	return extract.CueCall{Tool: c.Name, Args: args, Output: strings.Join(out, "\n")}
}

// CueRates replays cfg's seeds and counts.
func CueRates(cfg Config) CueTally {
	var t CueTally
	for _, seed := range cfg.Seeds {
		t.Seeds++
		opt := cfg.Options
		opt.Turns = cfg.Turns
		script := Generate(seed, opt)
		var branch []session.Entry
		fold := ledger.FoldedLedger{}
		for i, turn := range script.Turns {
			if i > 0 && i%CueCompactionEvery == 0 {
				branch = append(branch, session.Entry{Type: "compaction", ID: fmt.Sprintf("k%d", i), Summary: "s", FirstKeptEntryID: UserID(i)})
			}
			start := len(branch)
			branch = append(branch, TurnEntries(turn, i)...)
			for _, c := range turn.Calls {
				call := extract.CueCall{Tool: c.Name, Args: c.Args, Output: c.Result}
				t.Calls++
				if _, ok := extract.CueOver(branch[:start], &fold, call, cueCwd, 1<<30); ok {
					t.Eligible++
				}
				if cue, ok := extract.CueOver(branch, &fold, call, cueCwd, extract.CueCap); ok {
					t.Cued++
					branch = append(branch, session.Entry{Type: "custom", ID: fmt.Sprintf("q%d-%d", i, t.Cued), CustomType: ledger.OMCue,
						Data: map[string]any{"record": cue.Record, "key": cue.Key, "compaction": ""}})
				}
				t.LookAlikes++
				if _, ok := extract.CueOver(branch[:start], &fold, lookAlike(c), cueCwd, 1<<30); ok {
					t.FalseCues++
				}
			}
			folded := ledger.FoldLedger(branch, "")
			r := extract.Extract(extract.Input{Entries: branch, Start: ledger.ExtractionStartIndex(branch), End: len(branch), Ledger: folded, Cwd: cueCwd})
			for n, b := range r.Batches {
				if data := ledger.BuildObservationsRecordedData(b.Observations, b.CoversUpToID); data != nil {
					branch = append(branch, session.Entry{Type: "custom", ID: fmt.Sprintf("x%d-%d", i, n), CustomType: ledger.OMObservationsRecorded, Data: jsonData(data)})
				}
			}
			if r.CoversUpToID != "" {
				branch = append(branch, session.Entry{Type: "custom", ID: fmt.Sprintf("e%d", i), CustomType: ledger.OMExtracted, Data: map[string]any{"coversUpToId": r.CoversUpToID}})
			}
			fold = ledger.FoldLedger(branch, "")
		}
		for _, o := range fold.ActiveObservations {
			c, ok := repeatCall(o)
			if !ok || !extract.Cueable(o) {
				continue
			}
			call := extract.CueCall{Tool: c.Name, Args: c.Args, Output: c.Result}
			t.Repeats++
			if _, ok := extract.CueOver(branch, &fold, call, cueCwd, 1<<30); ok {
				t.RepeatCued++
			}
			t.RepeatLookAlikes++
			if _, ok := extract.CueOver(branch, &fold, lookAlike(c), cueCwd, 1<<30); ok {
				t.RepeatFalse++
			}
		}
	}
	t.Capped = t.Eligible - t.Cued
	if t.Capped < 0 {
		t.Capped = 0
	}
	return t
}

// jsonData is v as decoded JSON, the shape an entry read back from a session holds.
func jsonData(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

// cueCwd is the replay's working directory.
const cueCwd = "/work/app"

// Table prints the tally.
func (t CueTally) Table() string {
	pct := func(a, b int) string {
		if b == 0 {
			return "0%"
		}
		return fmt.Sprintf("%.1f%%", 100*float64(a)/float64(b))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "| measure | count | rate |\n|---|---|---|\n")
	fmt.Fprintf(&b, "| tool calls replayed | %d | |\n", t.Calls)
	fmt.Fprintf(&b, "| calls an exact key of a standing record matches | %d | %s of calls |\n", t.Eligible, pct(t.Eligible, t.Calls))
	fmt.Fprintf(&b, "| cues given (cap %d per compaction, once per record) | %d | %s of calls (cue rate), %s of matched calls |\n", extract.CueCap, t.Cued, pct(t.Cued, t.Calls), pct(t.Cued, t.Eligible))
	fmt.Fprintf(&b, "| look-alike calls (one character changed per key) | %d | |\n", t.LookAlikes)
	fmt.Fprintf(&b, "| cues a look-alike got | %d | %s (false-cue rate) |\n", t.FalseCues, pct(t.FalseCues, t.LookAlikes))
	fmt.Fprintf(&b, "| calls repeating a standing cueable record's key at the session's end | %d | |\n", t.Repeats)
	fmt.Fprintf(&b, "| cues they got | %d | %s |\n", t.RepeatCued, pct(t.RepeatCued, t.Repeats))
	fmt.Fprintf(&b, "| cues their look-alikes got | %d | %s (false-cue rate) |\n", t.RepeatFalse, pct(t.RepeatFalse, t.RepeatLookAlikes))
	return b.String()
}
