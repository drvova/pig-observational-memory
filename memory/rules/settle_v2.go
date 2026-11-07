package rules

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/tokens"
)

// Decision 24's settlement rules (docs/brain-memory.md; docs/memory-v2.md, "Settling a
// definition"), all code over the usage ledger's compaction passes, so none adds a request.
// lisp/src/layer/settle.lisp is the image's port, checked byte for byte (TestLispSettleParity).
//
// A unit is a memory v2 compaction pass that is not empty and carries the harm signal
// (curated.ModelEvent.Harm; passes recorded before these rules carry none and are no evidence).
// A unit is independent when one of its evidence roots (curated.ModelEvent.Roots: digests of the
// records citing its cut range that are not tool output) was in no earlier unit, so compactions
// over the same text count once, whichever session recorded them. For held definition d:
//
//   - its treated units are the independent units after its hold whose ranHeld names its held
//     text; its controls the independent units where no text of its name ran (held or written);
//     a consolidation instructions definition counts only units whose level-1 request answered;
//   - it is decided once MinEvidence treated units were counted: the first MinEvidence are the
//     treated window T, and the last 2·MinEvidence controls before T's end are two control
//     windows, B then A. Fewer controls than that is no null arm, and it is rejected;
//   - harm: T's harm above both A's and B's rejects it;
//   - effect: min(F(A), F(B)) - F(T), F the failures its kind is judged by (FailureCounts),
//     after its cost must exceed twice the A-versus-A spread |F(A) - F(B)|, and be at least one
//     failure when the spread is 0. Its cost is the tokens it adds to each request it runs in
//     (an instructions text's estimate, CostTokensPerFailure to a failure) over T, and the turns
//     T took beyond both control windows (CostTurnsPerFailure to a failure); otherwise it is
//     rejected, "no net effect after cost";
//   - 2·MinEvidence units under it with fewer than MinEvidence independent ones reject it: its
//     evidence came from too few roots;
//   - a written one's scope is the repositories and task families of T (layer.Scope).

// HarmCounts are the counts a unit's harm signal sums: a record dropped and then asked for, a
// level-1 proposal that would have lost a specific, and a fact the union rule had to keep back.
var HarmCounts = []string{DetectDroppedThenQueried, CountLevel1SpecificsRefused, CountLevel1UnionKept}

// instructionsFailures are the failures a consolidation instructions definition is judged by:
// a level-1 output nothing was recorded from, and proposals validation refused.
var instructionsFailures = []string{CountLevel1Failed, CountLevel1Refused}

// detectorFailures are the failures every other definition is judged by: the kernel detectors'
// counts but dropped-then-queried, which is harm.
var detectorFailures = []string{DetectUncitedAssistantSpecifics, DetectCrossScopeSupersede, DetectDecodeFailure}

// FailureCounts are the counts kind's definitions are judged by.
func FailureCounts(kind string) []string {
	if kind == layer.KindInstructions {
		return instructionsFailures
	}
	return detectorFailures
}

// The cost rates: CostTokensPerFailure added request tokens per compaction, or
// CostTurnsPerFailure added turns, weigh as much as one failure.
const (
	CostTokensPerFailure = 1000
	CostTurnsPerFailure  = 10
)

// MaxRoots bounds a unit's evidence roots: the first MaxRoots in order.
const MaxRoots = 64

// Unit is what a compaction pass records for settlement from the records citing its cut range:
// their evidence roots, and its scope (Repo, Family).
type Unit struct {
	Roots        []string
	Repo, Family string
}

// RootOf is a record's evidence root: the first 16 hex characters of the SHA-256 of
// "root", its kind and its content, NUL-separated. Its session, entry ids and writer never enter,
// so the same text read in two sessions, or twice, is one root.
func RootOf(o ledger.Observation) string {
	return layer.Digest("root\x00" + string(o.Kind) + "\x00" + o.Content)
}

// toolOutput reports a record of tool-output or untrusted origin, which is never evidence for a
// definition (decision 24, independent recurrence; scenario 10).
func toolOutput(o ledger.Observation) bool {
	return o.Source == ledger.SourceToolObserved || o.Source == ledger.SourceExternalUntrusted
}

// UnitOf is the unit of the records in observations that cite an entry in cut: the roots of the
// ones that are not tool output (sorted, at most MaxRoots), the commonest frame.repoId of all of
// them, and the commonest lower-cased extension of their paths keys (ties to the smaller). It is
// pure.
func UnitOf(observations []ledger.Observation, cut map[string]bool) Unit {
	roots := map[string]bool{}
	repos, families := map[string]int{}, map[string]int{}
	for _, o := range observations {
		if !slices.ContainsFunc(o.SourceEntryIDs, func(id string) bool { return cut[id] }) {
			continue
		}
		if !toolOutput(o) {
			roots[RootOf(o)] = true
		}
		if o.Frame.RepoID != "" {
			repos[o.Frame.RepoID]++
		}
		for _, p := range o.Keys.Paths {
			if ext := pathExt(p); ext != "" {
				families[ext]++
			}
		}
	}
	u := Unit{Repo: commonest(repos), Family: commonest(families)}
	for r := range roots {
		u.Roots = append(u.Roots, r)
	}
	sort.Strings(u.Roots)
	if len(u.Roots) > MaxRoots {
		u.Roots = u.Roots[:MaxRoots]
	}
	return u
}

// pathExt is path.Ext of p's last element, ASCII lower-cased.
func pathExt(p string) string {
	slash := strings.LastIndexByte(p, '/')
	dot := strings.LastIndexByte(p, '.')
	if dot < 0 || dot < slash {
		return ""
	}
	b := []byte(p[dot:])
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// commonest is the key with the highest count, ties to the smaller; "" for none.
func commonest(counts map[string]int) string {
	best, n := "", 0
	for k, c := range counts {
		if c > n || c == n && k < best {
			best, n = k, c
		}
	}
	return best
}

// HarmOf is a pass's harm signal from its counts (HarmCounts summed).
func HarmOf(counts map[string]int) int {
	n := 0
	for _, k := range HarmCounts {
		n += counts[k]
	}
	return n
}

// Settlement is what SettleV2 decided for a held definition: nothing yet (Decided false), written
// (Clean) with the scope its evidence certified, or reverted, each with its reason.
type Settlement struct {
	Decided, Clean bool
	Reason         string
	Scope          *layer.Scope
}

// costTokens is the tokens d adds to each request it runs in: a consolidation instructions
// definition's text as declared; nothing for the rest, which change only what code counts or
// keeps.
func costTokens(d layer.Definition) int {
	if d.Kind == layer.KindInstructions {
		return tokens.EstimateStringTokens(d.Declared())
	}
	return 0
}

type settleUnit struct {
	failures, harm, turns int
	repo, family          string
}

// settleNullArm is decision 24's rule for held definition d with held id id (above).
func settleNullArm(events []curated.ModelEvent, d layer.Definition, id string) Settlement {
	prefix := d.Key() + "@"
	failureKeys := FailureCounts(d.Kind)
	seen := map[string]bool{}
	var treated, controls []settleUnit
	underIt, decidedAt := 0, -1
	for _, e := range events {
		if !isV2Compaction(e) || e.Outcome == curated.OutcomeEmpty || e.Harm == nil {
			continue
		}
		if d.Kind == layer.KindInstructions && !e.Level1 {
			continue
		}
		at, ok := eventTime(e)
		if !ok {
			continue
		}
		independent := false
		for _, r := range e.Roots {
			if !seen[r] {
				independent = true
				seen[r] = true
			}
		}
		u := settleUnit{harm: *e.Harm, turns: e.Turns, repo: e.Repo, family: e.Family}
		for _, k := range failureKeys {
			u.failures += e.Counts[k]
		}
		ran := slices.ContainsFunc(e.RanHeld, func(s string) bool { return strings.HasPrefix(s, prefix) }) ||
			slices.ContainsFunc(e.RanWritten, func(s string) bool { return strings.HasPrefix(s, prefix) })
		switch {
		case slices.Contains(e.RanHeld, id) && at.After(d.Time):
			underIt++
			if independent {
				treated = append(treated, u)
			}
		case !ran && independent:
			controls = append(controls, u)
		}
		if len(treated) == MinEvidence {
			decidedAt = len(controls)
			break
		}
		if underIt >= 2*MinEvidence {
			break
		}
	}
	if decidedAt < 0 {
		if underIt >= 2*MinEvidence {
			return Settlement{Decided: true, Reason: fmt.Sprintf("its %d compactions came from %d independent evidence %s (needs %d): repeated text is one root",
				underIt, len(treated), pluralWord(len(treated), "root"), MinEvidence)}
		}
		return Settlement{}
	}
	if decidedAt < 2*MinEvidence {
		return Settlement{Decided: true, Reason: fmt.Sprintf("no null arm: %d compactions without it to compare against (needs %d)", decidedAt, 2*MinEvidence)}
	}
	b, a := controls[decidedAt-2*MinEvidence:decidedAt-MinEvidence], controls[decidedAt-MinEvidence:decidedAt]
	sum := func(us []settleUnit) (f, h, t int) {
		for _, u := range us {
			f, h, t = f+u.failures, h+u.harm, t+u.turns
		}
		return f, h, t
	}
	fT, hT, tT := sum(treated)
	fA, hA, tA := sum(a)
	fB, hB, tB := sum(b)
	if hT > max(hA, hB) {
		return Settlement{Decided: true, Reason: fmt.Sprintf("harm: %d over %d compactions under it, against %d and %d in two windows without it", hT, MinEvidence, hA, hB)}
	}
	effect := min(fA, fB) - fT
	spread := fA - fB
	if spread < 0 {
		spread = -spread
	}
	addedTurns := max(0, tT-max(tA, tB))
	cost := costTokens(d)*MinEvidence*1000/CostTokensPerFailure + addedTurns*1000/CostTurnsPerFailure
	figures := fmt.Sprintf("failures %d under it over %d compactions against %d and %d without it (spread %d), cost %d.%03d",
		fT, MinEvidence, fA, fB, spread, cost/1000, cost%1000)
	if net := effect*1000 - cost; net <= 2*spread*1000 || net < 1000 {
		return Settlement{Decided: true, Reason: "no net effect after cost: " + figures}
	}
	scope := &layer.Scope{}
	for _, u := range treated {
		if !slices.Contains(scope.Repos, u.repo) {
			scope.Repos = append(scope.Repos, u.repo)
		}
		if !slices.Contains(scope.Families, u.family) {
			scope.Families = append(scope.Families, u.family)
		}
	}
	sort.Strings(scope.Repos)
	sort.Strings(scope.Families)
	return Settlement{Decided: true, Clean: true, Reason: "its effect beat the null arm after cost: " + figures, Scope: scope}
}

// Targets are what d acts on (decision 24, the active set): its kind and name, and for a rule
// its detector and its remedy, so two definitions that share one are judged together.
func Targets(d layer.Definition) []string {
	out := []string{d.Key()}
	if IsRule(d.Kind, d.Name) {
		if r, err := parse(d.Kind+"/"+d.Name, d.Text); err == nil {
			out = append(out, layer.SeedKey(layer.KindDetector, r.Detector), layer.SeedKey(layer.KindRemedy, r.Remedy))
		}
	}
	return out
}

// ShareTarget reports whether a and b, two definitions, act on one target.
func ShareTarget(a, b layer.Definition) bool {
	tb := Targets(b)
	for _, t := range Targets(a) {
		if slices.Contains(tb, t) {
			return true
		}
	}
	return false
}
