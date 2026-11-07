package hooks

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/agents"
	"github.com/drvova/pig-observational-memory/memory/codepolicy"
	"github.com/drvova/pig-observational-memory/memory/consolidate"
	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/debuglog"
	"github.com/drvova/pig-observational-memory/memory/declare"
	"github.com/drvova/pig-observational-memory/memory/harness"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/insecure"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/rules"
)

// Definitions proposed only in level 1 (docs/memory-v2.md, "Self-built harness"). Under memory
// v2 the build and revision passes stay off: the session model defines part of its own harness
// only inside the compaction's level-1 request, and only when code found its harness evidence
// changed since the last offer (rules.ProposalDue), so no request is ever made for a definition.
// What it defines is checked like every definition from the model (rules.CheckV2Definition, the
// layer's checks, rules.CheckFromModel, harness.CheckModelDefinition) and held; a later
// compaction settles it on evidence its proposing request did not produce (rules.SettleV2).

// definitionOfferV2 is what the session model's level-1 request may define, with the evidence
// it is shown: nil without a store, while the evidence is unchanged since the last offer, or
// while a definition held under v2 waits for its proof (rules.ProposalDue). active is the
// model's layer the compaction read, whose definitions the offer lists.
func definitionOfferV2(ctx context.Context, store curated.Store, on bool, model string, active declare.Active) (*consolidate.DefinitionOffer, rules.Evidence) {
	if !on || model == "" {
		return nil, rules.Evidence{}
	}
	evidence, due, err := rules.ProposalDue(store, model)
	if err != nil {
		debuglog.Log(ctx, "harness.v2.proposal_check_failed", map[string]any{"model": model, "errorMessage": err.Error()})
		return nil, rules.Evidence{}
	}
	if !due {
		return nil, rules.Evidence{}
	}
	return &consolidate.DefinitionOffer{
		Kinds:     rules.V2DefinitionKinds,
		Detectors: rules.V2Detectors,
		Remedies:  rules.V2Remedies,
		Evidence:  harnessEvidenceText(evidence, active),
	}, evidence
}

// harnessEvidenceText is the harness evidence the level-1 input shows: what code counted over
// the model's last compactions and its v2 rules (rules.Evidence.Text), the definitions memory v2
// runs with that stand for it now (its own, written or held; the seeds otherwise), and what it
// may define.
func harnessEvidenceText(evidence rules.Evidence, active declare.Active) string {
	var b strings.Builder
	b.WriteString(evidence.Text())
	own := []string{}
	for _, d := range active.Harness().List("") {
		if d.Model == active.Model() && rules.SettlesUnderV2(d) {
			own = append(own, fmt.Sprintf("- %s/%s (%s)", d.Kind, d.Name, d.State))
		}
	}
	if len(own) == 0 {
		b.WriteString("Your definitions: none; every name stands as its seed\n")
	} else {
		b.WriteString("Your definitions:\n" + strings.Join(own, "\n") + "\n")
	}
	fmt.Fprintf(&b, "You may define: %s (instructions only %q, added after the kernel's rules and the seed, which they extend and never replace); rules: a detector of %s with a remedy of %s.",
		strings.Join(rules.V2DefinitionKinds, ", "), agents.InstructionsConsolidation, strings.Join(rules.V2Detectors, ", "), strings.Join(rules.V2Remedies, ", "))
	b.WriteString("\n" + detectorFormat)
	return b.String()
}

// detectorFormat tells level 1 what a detector definition is (docs/code-tool.md, "A detector is
// Go"): fixed text, the same for every model.
var detectorFormat = "A detector's text is one " + rules.DetectorTable + " row in the rows codec with a code column and a description, for example " +
	strconv.Quote(rules.CodeDetectorText("func Count() int { return om.Notes(om.Kind(`plan`)).Matched }", "plans recorded")) +
	": Go declarations with exactly one func " + codepolicy.DetectorEntry + "() int, reading only " + strings.Join(detectorReadNames(), ", ") +
	" with their options, and no writes. It runs after the compaction, in the background, over what the compaction removed, and is held only when that first run returns a count."

// detectorReadNames are the reads a detector calls.
func detectorReadNames() []string {
	out := []string{}
	for _, n := range []string{"Notes", "History", "Subjects", "NoteByID"} {
		out = append(out, "om."+n)
	}
	return out
}

// holdDefinitionsV2 holds what one level-1 response defined for model, once validation found it
// structurally valid (result.Definitions, result.Rules), and marks the offer
// (layer.OpPropose, with the evidence it was shown) in the same critical section as the holds
// (layer.Store.HoldPass), so the next offer waits for changed evidence even when nothing was
// defined. Each definition passes rules.CheckV2Proposal (a kind v2 judges; the consolidation
// instructions only extend the seed), is refused when identical to its
// seed, and passes harness.CheckModelDefinition (the layer's checks and rules.CheckFromModel
// against the branch entries); each rule goes through rules.Propose, which holds it for v2.
// A Go detector is not held here: it is returned as pending, smoke-run first in the compaction's
// detector pass, and held only when that run returns a count. Nothing is held or marked once the
// request context is done. The user is told of each hold, pending detector and refusal. It
// returns how many were held and refused, and the pending detectors.
func holdDefinitionsV2(ctx, request context.Context, h host.Host, store curated.Store, model string, entries []ledger.Entry, result consolidate.Result, evidence rules.Evidence) (held, refused int, pending []layer.Definition) {
	defs := []layer.Definition{}
	refuse := func(what string, err error) {
		refused++
		debuglog.Log(ctx, "harness.v2.definition_refused", map[string]any{"model": model, "definition": what, "errorMessage": err.Error()})
		notifyUI(h, fmt.Sprintf("Observational memory: level-1 definition %s for %s refused: %v", what, model, err), "warning")
	}
	// One proposal holds one rule, never a bundle (decision 24, the active set): the first
	// definition or rule that passes its checks takes the proposal, and the rest wait for a later
	// offer.
	taken := ""
	take := func(what string) bool {
		if taken != "" {
			refuse(what, fmt.Errorf("%s: %s already takes this proposal", errBundle, taken))
			return false
		}
		taken = what
		return true
	}
	insecureHeld := 0
	for _, p := range result.Definitions {
		d := layer.Definition{Kind: p.Kind, Name: p.Name, Model: model, Text: p.Text, Session: h.SessionID()}
		what := p.Kind + "/" + p.Name
		if err := rules.CheckV2Proposal(d.Kind, d.Name, d.Text); err != nil {
			refuse(what, err)
			continue
		}
		if sameAsSeed(d) {
			refuse(what, errors.New("identical to the seed, which already stands; define only what you would change"))
			continue
		}
		if err := harness.CheckModelDefinition(store, entries, d); err != nil {
			refuse(what, err)
			continue
		}
		if !take(what) {
			continue
		}
		if rule, ok := insecure.Match(d.Text); ok {
			// Decision 19 over definitions: one that weakens security is kept out of the layer,
			// listed in /om:status, and never run, settled or proposed over.
			if request.Err() != nil {
				continue
			}
			if _, err := store.Layer().HoldInsecure(d, rule); err != nil {
				debuglog.Log(ctx, "harness.v2.hold_insecure_failed", map[string]any{"model": model, "definition": what, "errorMessage": err.Error()})
				continue
			}
			insecureHeld++
			debuglog.Log(ctx, "harness.v2.hold_insecure", map[string]any{"model": model, "definition": what, "rule": rule})
			notifyUI(h, fmt.Sprintf("Observational memory: level-1 definition %s for %s weakens security (%s); held, never applied, and listed in /om:status", what, model, rule), "warning")
			continue
		}
		if isCodeDetector(d) {
			// A Go detector is held only once its smoke run over this compaction's own pass
			// returns a count, in the background after the compaction (startDetectorPassV2).
			pending = append(pending, d)
			continue
		}
		defs = append(defs, d)
	}
	if request.Err() != nil {
		return 0, refused, nil
	}
	heldDefs, storeRefused, _, err := store.Layer().HoldPass(model, defs, layer.Generation{Op: layer.OpPropose, Evidence: evidence.Digest(), Events: evidence.Events, Session: h.SessionID(), Reason: "level 1"})
	if err != nil {
		debuglog.Log(ctx, "harness.v2.proposal_mark_failed", map[string]any{"model": model, "errorMessage": err.Error()})
	}
	for i, e := range storeRefused {
		if e != nil {
			refuse(defs[i].Kind+"/"+defs[i].Name, e)
		}
	}
	for _, d := range heldDefs {
		held++
		notifyUI(h, fmt.Sprintf("Observational memory: %s defined %s/%s in level 1 (%s); held until a later compaction proves it", model, d.Kind, d.Name, d.Relation()), "info")
	}
	held += insecureHeld
	for _, p := range result.Rules {
		if request.Err() != nil {
			break
		}
		what := "rule " + p.Detector + "/" + p.Remedy
		if taken != "" {
			refuse(what, fmt.Errorf("%s: %s already takes this proposal", errBundle, taken))
			continue
		}
		r, err := rules.Propose(store, model, entries, rules.Proposal{Detector: p.Detector, Remedy: p.Remedy})
		if err != nil {
			refuse(what, err)
			continue
		}
		taken = what
		held++
		notifyUI(h, fmt.Sprintf("Observational memory: %s proposed rule %s in level 1; held until a later compaction proves it, then judged by %s", model, r.Describe(), r.Detector), "info")
	}
	for _, d := range pending {
		notifyUI(h, fmt.Sprintf("Observational memory: %s defined detector %s in level 1; its smoke run over this compaction follows in the background, and only a count holds it", model, d.Name), "info")
	}
	debuglog.Log(ctx, "harness.v2.proposal", map[string]any{"model": model, "held": held, "refused": refused, "pending": len(pending)})
	return held, refused, pending
}

// isCodeDetector reports whether d is a Go detector (its code column), which is smoke-run before
// it is held.
func isCodeDetector(d layer.Definition) bool {
	if d.Kind != layer.KindDetector {
		return false
	}
	def, err := rules.ParseDetector(d.Name, d.Text)
	return err == nil && def.IsCode()
}

// sameAsSeed reports whether d's text (an instructions body after its mode line) is its seed's,
// which changes nothing.
func sameAsSeed(d layer.Definition) bool {
	text := d.Text
	if d.Kind == layer.KindInstructions {
		if _, body, err := layer.SplitMode(text); err == nil {
			text = body
		}
	}
	seed, ok := layer.Shipped()[layer.SeedKey(d.Kind, d.Name)]
	return ok && strings.TrimSpace(seed) == strings.TrimSpace(text)
}

// settleDefinitionsV2 settles the held definitions of the ledger's model that the compaction ran
// with (snapshot, its layer read before level 1, so a definition this compaction's level 1 held
// is never settled by it) and memory v2 settles (rules.SettlesUnderV2), now that the compaction's
// pass is recorded: each one rules.SettleV2 decides is written when clean and reverted otherwise
// (layer.Store.ClaimHeld, only while its held file still holds the text the compaction ran
// with), and the user is told of each. One with no evidence yet stays held. Nothing is settled
// once ctx is done.
func settleDefinitionsV2(ctx context.Context, h host.Host, l harness.Ledger, snapshot layer.Harness) {
	var events []curated.ModelEvent
	loaded := false
	for _, d := range snapshot.List("") {
		if d.State != layer.StateHeld || d.Model != l.Model || !rules.SettlesUnderV2(d) {
			continue
		}
		if !loaded {
			var err error
			if events, err = l.Store.ModelEvents(l.Model); err != nil {
				debuglog.Log(ctx, "harness.v2.settle_failed", map[string]any{"model": l.Model, "errorMessage": err.Error()})
				return
			}
			loaded = true
		}
		s := rules.SettleV2(events, d)
		if !s.Decided {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		notices, err := l.Store.Layer().ClaimHeldScoped([]layer.Definition{d}, s.Clean, s.Reason, s.Scope)
		if err != nil {
			debuglog.Log(ctx, "layer.claim_failed", map[string]any{"stage": rules.StageCompaction, "errorMessage": err.Error()})
		}
		for _, n := range notices {
			debuglog.Log(ctx, "layer.claim", map[string]any{"model": n.Model, "kind": n.Kind, "name": n.Name, "written": n.Written, "reason": s.Reason})
			notifyUI(h, n.Message(), "info")
			if n.Written {
				recheckActiveSetV2(ctx, h, l, d)
			}
		}
	}
}

// errBundle is the refusal of a definition after the first in one level-1 proposal.
var errBundle = errors.New("one proposal holds one rule, never a bundle; propose it at a later offer")

// recheckActiveSetV2 returns the model's other written definitions that share a target with d,
// which just settled (rules.ShareTarget: the same instruction slot, detector or remedy), to a
// hold window (layer.Store.Recheck): the next compactions run them held beside d, so their
// settle check reads the evidence of the set as it now stands (decision 24, the active set).
func recheckActiveSetV2(ctx context.Context, h host.Host, l harness.Ledger, d layer.Definition) {
	ls := l.Store.Layer()
	written, _, err := ls.List(l.Model, "")
	if err != nil {
		debuglog.Log(ctx, "harness.v2.recheck_failed", map[string]any{"model": l.Model, "errorMessage": err.Error()})
		return
	}
	for _, w := range written {
		if w.Key() == d.Key() || !settlesWritten(w) || !rules.ShareTarget(d, w) {
			continue
		}
		held, ok, err := ls.Recheck(l.Model, w.Kind, w.Name)
		if err != nil {
			debuglog.Log(ctx, "harness.v2.recheck_failed", map[string]any{"model": l.Model, "definition": w.Key(), "errorMessage": err.Error()})
			continue
		}
		if ok {
			debuglog.Log(ctx, "harness.v2.recheck", map[string]any{"model": l.Model, "definition": held.Key(), "settled": d.Key()})
			notifyUI(h, fmt.Sprintf("Observational memory: %s shares a target with %s, which settled; held again for %s so the set is judged together", held.Key(), d.Key(), l.Model), "info")
		}
	}
}

// settlesWritten reports whether written definition w is one memory v2 would settle held: of a
// kind it judges, or a rule it decides.
func settlesWritten(w layer.Definition) bool {
	w.Engine = curated.EngineV2
	return rules.SettlesUnderV2(w)
}

// rollbackInsecureV2 rolls the model's layer back to the generation before any written
// definition of its own that matches the security-weakening patterns (contracts/insecure.md;
// decision 24, rollback), the rules kept as they are: a definition that entered before the
// patterns held it, or by another path. It is code acting on code evidence, never a model's or a
// tool's request, so the quote gate (decision 2) does not apply; the generation records why, and
// /om:status lists it.
func rollbackInsecureV2(ctx context.Context, h host.Host, l harness.Ledger) {
	ls := l.Store.Layer()
	for range 8 {
		written, _, err := ls.List(l.Model, "")
		if err != nil {
			return
		}
		hit := -1
		rule := ""
		for i, w := range written {
			if r, ok := insecure.Match(w.Text); ok && ctx.Err() == nil {
				hit, rule = i, r
				break
			}
		}
		if hit < 0 {
			return
		}
		w := written[hit]
		reason := fmt.Sprintf("security: %s weakens security (%s); rolled back to the generation before it entered, on code evidence (no model or tool request, so no quote gate applies)", w.Key(), rule)
		g, ok, err := ls.RollbackBefore(l.Model, w.Kind, w.Name, reason, rules.IsRule)
		if err != nil || !ok {
			debuglog.Log(ctx, "harness.v2.security_rollback_failed", map[string]any{"model": l.Model, "definition": w.Key(), "errorMessage": fmt.Sprint(err)})
			return
		}
		debuglog.Log(ctx, "harness.v2.security_rollback", map[string]any{"model": l.Model, "definition": w.Key(), "rule": rule, "generation": g.N})
		notifyUI(h, fmt.Sprintf("Observational memory: %s's layer: %s", l.Model, g.Describe()), "warning")
	}
}
