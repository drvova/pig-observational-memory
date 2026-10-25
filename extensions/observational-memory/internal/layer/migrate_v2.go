package layer

import (
	"fmt"
	"slices"
	"strings"
)

// OpMigrateV2 records what memory v2 did with one of v1's written worker instructions
// (MigrateToV2, docs/memory-v2.md, "Migration > Layer definitions"): folded into the
// consolidation instructions as an addition, or retired. Kind and Name are the v1 definition's,
// Digest the content it had then (already kept in the blobs), Reason what was done. It changes
// no file, so the written layer v1 reads is unchanged; it marks that content migrated, so the
// same text is never folded twice.
const OpMigrateV2 = "migrate-v2"

// V2Migration is one v1 instructions definition MigrateToV2 folded, removed from the fold or
// retired.
type V2Migration struct {
	Model, Name string
	// From is the model whose v1 definition it is: Model, or AllModels for an addition Model
	// inherits from the one kept for all models.
	From string
	// Folded marks a text set as its stage's block of Into's addition; Removed marks a block
	// taken out of it because v1's addition is gone or no longer one; otherwise it was retired,
	// with Why.
	Folded, Removed bool
	Into            string
	Why             string
	// Generation is the generation that wrote Into (a fold or a removal) or marked the
	// retirement.
	Generation int
}

// Message is the migration as the user is told it.
func (m V2Migration) Message() string {
	what := fmt.Sprintf("the %s/%s addition for %s", KindInstructions, m.Name, m.Model)
	if m.From != "" && m.From != m.Model {
		what = fmt.Sprintf("the %s/%s addition kept for all models", KindInstructions, m.Name)
	}
	switch {
	case m.Folded:
		return fmt.Sprintf("Observational memory: memory v2 folded %s into the %s/%s instructions for %s as an addition (generation %d); memory v2 runs no %s, and its own text is kept as it was for memory v1", what, KindInstructions, m.Into, m.Model, m.Generation, m.Name)
	case m.Removed:
		return fmt.Sprintf("Observational memory: memory v2 removed the %s block from the %s/%s instructions for %s (generation %d): memory v1's %s addition is gone or no longer an addition", m.Name, KindInstructions, m.Into, m.Model, m.Generation, m.Name)
	}
	return fmt.Sprintf("Observational memory: memory v2 retires the %s/%s definition for %s (%s, generation %d): memory v2 runs no %s, so it is not used; its text is kept in the layer for memory v1", KindInstructions, m.Name, m.Model, m.Why, m.Generation, m.Name)
}

// The reasons a v1 instructions definition is retired under memory v2.
const (
	v2RetiredReplace = "a replacement of its seed, which memory v2 has no stage for"
	v2RetiredStage   = "instructions of a stage memory v2 does not run"
)

// foldHeader and foldFooter delimit the block a v1 stage's addition is folded into: the block
// is found again by them, so a later migration replaces or removes it, never repeats it.
func foldHeader(name string) string {
	return fmt.Sprintf("From the %s instructions (memory v1):", name)
}
func foldFooter(name string) string {
	return fmt.Sprintf("End of the %s instructions (memory v1).", name)
}

// withFoldBlock is text with name's folded block set to body (in place when it is there, else
// appended), or removed when body is nil. A header with no footer after it is not a block.
func withFoldBlock(text, name string, body *string) string {
	join := func(parts ...string) string {
		kept := []string{}
		for _, p := range parts {
			if p != "" {
				kept = append(kept, p)
			}
		}
		return strings.Join(kept, "\n\n")
	}
	block := ""
	if body != nil {
		block = foldHeader(name) + "\n" + *body + "\n" + foldFooter(name)
	}
	if i := strings.Index(text, foldHeader(name)); i >= 0 {
		if j := strings.Index(text[i:], foldFooter(name)); j >= 0 {
			before := strings.TrimRight(text[:i], "\n")
			after := strings.TrimLeft(text[i+j+len(foldFooter(name)):], "\n")
			return join(before, block, after)
		}
	}
	if body == nil {
		return text
	}
	return join(strings.TrimRight(text, "\n"), block)
}

// MigrateToV2 migrates model's written v1 worker instructions to memory v2: each extend name's
// effective addition (ModeExtend, or recorded before modes) is set as its stage's block of
// model's into instructions (the consolidation instructions), between a line naming the stage
// and one closing it, written as one generation; a replace-mode text of an extend name, and
// every retire name's text, is retired. The effective addition is model's own, or, for a model
// that does not define the name, the one kept for all models: a model's own into definition
// replaces the all-models one entirely, so it must carry what v1 had the model inherit. A
// model that defines no into of its own and folds nothing of its own writes none and keeps
// inheriting the all-models one. Each migrated content is marked with an OpMigrateV2
// generation, so it is migrated once; a later edit under v1 is migrated again and replaces its
// stage's block, and a block whose addition v1 no longer has (forgotten, rolled back, now a
// replacement) is removed. A block the model's own definitions took out is not put back. The
// v1 files are left as they are: v1 still reads the written layer, which both engines share,
// so they stand for v1 and are unused under v2. Only files the store recorded and its checks
// accept are read, and a held definition (an engine's own) is never touched. While into has a
// held definition the folds wait, so a settlement never reverts the fold away; a fold the
// store's checks refuse retires its texts with the refusal. It returns the folds, the
// removals, then the retirements.
func (s Store) MigrateToV2(model, into string, extend, retire []string) ([]V2Migration, error) {
	var out []V2Migration
	err := s.locked(func() error {
		recorded, err := s.current(model)
		if err != nil {
			return err
		}
		inherited := map[string]string{}
		if model != AllModels {
			if inherited, err = s.current(AllModels); err != nil {
				return err
			}
		}
		gens, err := s.Generations(model)
		if err != nil {
			return err
		}
		migrated := func(name, digest string) bool {
			for _, g := range gens {
				if g.Op == OpMigrateV2 && g.Kind == KindInstructions && g.Name == name && g.Digest == digest {
					return true
				}
			}
			return false
		}
		type source struct {
			def    Definition
			digest string
			own    bool
		}
		// effective is the v1 definition of name model runs under v1: its own, else the one
		// kept for all models.
		effective := func(name string) (source, bool) {
			key := SeedKey(KindInstructions, name)
			if d, _, ok := s.recordedFile(s.Path(model, KindInstructions, name), model, name, recorded); ok {
				return source{def: d, digest: recorded[key], own: true}, true
			}
			if model != AllModels {
				if d, _, ok := s.recordedFile(s.Path(AllModels, KindInstructions, name), AllModels, name, inherited); ok {
					return source{def: d, digest: inherited[key]}, true
				}
			}
			return source{}, false
		}
		var fold, retired []source
		var gone []string
		ownFold := false
		why := map[string]string{}
		for _, name := range append(append([]string{}, extend...), retire...) {
			src, ok := effective(name)
			isExtend := slices.Contains(extend, name)
			if isExtend && (!ok || src.def.Replaces()) {
				gone = append(gone, name)
			}
			if !ok || migrated(name, src.digest) {
				continue
			}
			switch {
			case isExtend && !src.def.Replaces():
				fold = append(fold, src)
				ownFold = ownFold || src.own
			case !src.own:
				// The all-models definition's retirement is that model's to mark.
			case isExtend:
				retired, why[name] = append(retired, src), v2RetiredReplace
			default:
				retired, why[name] = append(retired, src), v2RetiredStage
			}
		}
		target, hasTarget := Definition{}, false
		if d, _, ok := s.recordedFile(s.Path(model, KindInstructions, into), model, into, recorded); ok {
			target, hasTarget = d, true
		}
		if !hasTarget && model != AllModels && !ownFold {
			// Nothing of the model's own: it keeps inheriting the all-models definition.
			fold, gone = nil, nil
		}
		if len(fold) > 0 || len(gone) > 0 {
			index, err := s.heldIndex(model)
			if err != nil {
				return err
			}
			if index[SeedKey(KindInstructions, into)] != "" {
				fold, gone = nil, nil
			}
		}
		if !hasTarget {
			target = Definition{Kind: KindInstructions, Name: into, Model: model, Mode: ModeExtend}
			if model != AllModels {
				if d, _, ok := s.recordedFile(s.Path(AllModels, KindInstructions, into), AllModels, into, inherited); ok {
					target.Text, target.Mode = d.Text, d.Mode
				}
			}
		}
		text := target.Text
		for _, src := range fold {
			body := src.def.Text
			text = withFoldBlock(text, src.def.Name, &body)
		}
		removed := []string{}
		for _, name := range gone {
			if next := withFoldBlock(text, name, nil); next != text {
				text, removed = next, append(removed, name)
			}
		}
		if len(fold) > 0 || len(removed) > 0 {
			target.Text = text
			target.Engine = ""
			stamped, err := s.stampLifted(target)
			if err != nil {
				for _, src := range fold {
					if src.own {
						retired, why[src.def.Name] = append(retired, src), "its text could not be folded into "+into+": "+err.Error()
					}
				}
				fold, removed = nil, nil
			} else {
				if _, err := s.putStamped(stamped); err != nil {
					return err
				}
				put := s.lastGeneration(model)
				for _, src := range fold {
					if _, err := s.appendGeneration(model, Generation{Op: OpMigrateV2, Kind: KindInstructions, Name: src.def.Name, Digest: src.digest, Reason: fmt.Sprintf("folded into %s/%s as an addition (generation %d)", KindInstructions, into, put)}); err != nil {
						return err
					}
					out = append(out, V2Migration{Model: model, From: src.def.Model, Name: src.def.Name, Folded: true, Into: into, Generation: put})
				}
				for _, name := range removed {
					out = append(out, V2Migration{Model: model, Name: name, Removed: true, Into: into, Generation: put})
				}
			}
		}
		for _, src := range retired {
			g, err := s.appendGeneration(model, Generation{Op: OpMigrateV2, Kind: KindInstructions, Name: src.def.Name, Digest: src.digest, Reason: "retired under memory v2: " + why[src.def.Name]})
			if err != nil {
				return err
			}
			out = append(out, V2Migration{Model: model, From: model, Name: src.def.Name, Why: why[src.def.Name], Generation: g.N})
		}
		return nil
	})
	return out, err
}

// lastGeneration is the number of model's latest generation, 0 for none; the caller holds the
// store's lock.
func (s Store) lastGeneration(model string) int {
	gens, err := s.Generations(model)
	if err != nil {
		return 0
	}
	return len(gens)
}
