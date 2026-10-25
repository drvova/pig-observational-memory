package hooks

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/consolidate"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/declare"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
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
	return b.String()
}

// holdDefinitionsV2 holds what one level-1 response defined for model, once validation found it
// structurally valid (result.Definitions, result.Rules), and marks the offer
// (layer.OpPropose, with the evidence it was shown) in the same critical section as the holds
// (layer.Store.HoldPass), so the next offer waits for changed evidence even when nothing was
// defined. Each definition passes rules.CheckV2Proposal (a kind v2 judges; the consolidation
// instructions only extend the seed), is refused when identical to its
// seed, and passes harness.CheckModelDefinition (the layer's checks and rules.CheckFromModel
// against the branch entries); each rule goes through rules.Propose, which holds it for v2.
// Nothing is held or marked once the request context is done. The user is told of each hold and
// refusal. It returns how many were held and refused.
func holdDefinitionsV2(ctx, request context.Context, h host.Host, store curated.Store, model string, entries []ledger.Entry, result consolidate.Result, evidence rules.Evidence) (held, refused int) {
	defs := []layer.Definition{}
	refuse := func(what string, err error) {
		refused++
		debuglog.Log(ctx, "harness.v2.definition_refused", map[string]any{"model": model, "definition": what, "errorMessage": err.Error()})
		notifyUI(h, fmt.Sprintf("Observational memory: level-1 definition %s for %s refused: %v", what, model, err), "warning")
	}
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
		defs = append(defs, d)
	}
	if request.Err() != nil {
		return 0, refused
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
	for _, p := range result.Rules {
		if request.Err() != nil {
			break
		}
		r, err := rules.Propose(store, model, entries, rules.Proposal{Detector: p.Detector, Remedy: p.Remedy})
		if err != nil {
			refuse("rule "+p.Detector+"/"+p.Remedy, err)
			continue
		}
		held++
		notifyUI(h, fmt.Sprintf("Observational memory: %s proposed rule %s in level 1; held until a later compaction proves it, then judged by %s", model, r.Describe(), r.Detector), "info")
	}
	debuglog.Log(ctx, "harness.v2.proposal", map[string]any{"model": model, "held": held, "refused": refused})
	return held, refused
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
		decided, clean, reason := rules.SettleV2(events, d)
		if !decided {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		notices, err := l.Store.Layer().ClaimHeld([]layer.Definition{d}, clean, reason)
		if err != nil {
			debuglog.Log(ctx, "layer.claim_failed", map[string]any{"stage": rules.StageCompaction, "errorMessage": err.Error()})
		}
		for _, n := range notices {
			debuglog.Log(ctx, "layer.claim", map[string]any{"model": n.Model, "kind": n.Kind, "name": n.Name, "written": n.Written, "reason": reason})
			notifyUI(h, n.Message(), "info")
		}
	}
}
