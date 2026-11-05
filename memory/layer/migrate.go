package layer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MigrationReason is the reason a migration generation (OpMigrate) records.
const MigrationReason = "recorded before instructions modes; read as an addition: the seed instructions stand again with its text appended after them"

// RepeatsSeedShare is the share of its seed's lines a migrated text must repeat to be named in
// its migration's notice as a near-copy of the seed (Migration.RepeatsSeed): written when an
// instructions definition took the seed's place, it was often an edited copy of the seed, and
// as an addition the prompt now carries the seed and that copy, with any rule it changed now
// contradicting the seed's.
const RepeatsSeedShare = 0.5

// Migration is one instructions definition MigrateInstructions made an addition.
type Migration struct {
	Model, Name string
	// Held marks a held definition (its held file was migrated).
	Held bool
	// Generation is the migration's generation.
	Generation int
	// RepeatsSeed marks a text that repeats at least RepeatsSeedShare of its seed's lines, and
	// Bytes is the text's length: what the prompt grew by.
	RepeatsSeed bool
	Bytes       int
}

// Message is the migration as the user is told it.
func (m Migration) Message() string {
	what := "written"
	if m.Held {
		what = "held"
	}
	msg := fmt.Sprintf("Observational memory: the %s %s/%s definition for %s was recorded before instructions modes and is now an addition: the seed instructions stand again with its text appended after them (generation %d; open it with \"%s %s\" to replace the seed)", what, KindInstructions, m.Name, m.Model, m.Generation, ModeLine, ModeReplace)
	if m.RepeatsSeed {
		return msg + fmt.Sprintf("; its text repeats most of the seed's lines, so the prompt now carries the seed and a near-copy of it (%d more bytes), and any seed rule it changed now meets the seed's own: define it again as only its changes, or with \"%s %s\"", m.Bytes, ModeLine, ModeReplace)
	}
	return msg + "; if it restated or contradicted the seed, the prompt now carries both"
}

// MigrateInstructions makes every instructions definition of model recorded before modes (no
// mode in its header) an addition (ModeExtend): it was written when an instructions definition
// replaced its seed, and is now read as one appended after the seed. Each written file is
// rewritten with the mode and its new content recorded as an OpMigrate generation (probation
// judges it afresh: what it was judged on ran in the seed's place); each held file is
// rewritten with its fallback, its held index entry updated, and an OpMigrate generation marked
// Held. A held file whose fallback names a written file this run migrated is rewritten to name
// the migrated content (whatever its own mode), so a failed pass puts back what stood, not the
// content from before the migration. Only files the store recorded and its checks accept are
// migrated (an untracked or refused one is never loaded anyway), and no other kind is touched.
// It is idempotent: a migrated file has a mode. It returns the migrations, in name order.
func (s Store) MigrateInstructions(model string) ([]Migration, error) {
	var out []Migration
	err := s.locked(func() error {
		recorded, err := s.current(model)
		if err != nil {
			return err
		}
		seeds := s.seeds()
		names, err := rowsNames(filepath.Join(s.modelPath(model), KindInstructions))
		if err != nil {
			return err
		}
		// moved maps each migrated written name to its content's digest before and after.
		type move struct{ from, to string }
		moved := map[string]move{}
		for _, name := range names {
			path := s.Path(model, KindInstructions, name)
			d, _, ok := s.recordedFile(path, model, name, recorded)
			if !ok || d.Mode != "" {
				continue
			}
			d.Mode = ModeExtend
			content := encodeFile(d, "")
			if err := s.write(model, path, content); err != nil {
				return err
			}
			g, err := s.appendGeneration(model, Generation{Op: OpMigrate, Kind: KindInstructions, Name: name, Digest: Digest(content), Session: d.Session, Reason: MigrationReason})
			if err != nil {
				return err
			}
			moved[name] = move{from: recorded[d.Key()], to: Digest(content)}
			out = append(out, migration(model, d, false, g.N, seeds))
		}
		index, err := s.heldIndex(model)
		if err != nil {
			return err
		}
		names, err = rowsNames(filepath.Join(s.modelPath(model), "held", KindInstructions))
		if err != nil {
			return err
		}
		for _, name := range names {
			path := s.heldPath(model, KindInstructions, name)
			d, fallback, ok := s.recordedFile(path, model, name, index)
			if !ok {
				continue
			}
			rewrite := false
			if m, ok := moved[name]; ok && fallback == m.from {
				fallback, rewrite = m.to, true
			}
			migrate := d.Mode == ""
			if migrate {
				d.Mode, rewrite = ModeExtend, true
			}
			if !rewrite {
				continue
			}
			content := encodeFile(d, fallback)
			if err := writeAtomic(path, content); err != nil {
				return err
			}
			if err := s.recordHeld(model, d.Key(), Digest(content)); err != nil {
				return err
			}
			if !migrate {
				continue
			}
			g, err := s.appendGeneration(model, Generation{Op: OpMigrate, Kind: KindInstructions, Name: name, Held: true, Session: d.Session, Reason: MigrationReason})
			if err != nil {
				return err
			}
			out = append(out, migration(model, d, true, g.N, seeds))
		}
		return nil
	})
	return out, err
}

// migration is the Migration of d, model's instructions definition, at generation n.
func migration(model string, d Definition, held bool, n int, seeds map[string]string) Migration {
	return Migration{Model: model, Name: d.Name, Held: held, Generation: n, Bytes: len(d.Text),
		RepeatsSeed: seedShare(seeds[d.Key()], d.Text) >= RepeatsSeedShare}
}

// seedShare is the share of seed's distinct non-blank lines (compared trimmed) that text
// repeats; 0 for an empty seed.
func seedShare(seed, text string) float64 {
	lines := map[string]bool{}
	for _, l := range strings.Split(text, "\n") {
		lines[strings.TrimSpace(l)] = true
	}
	seen := map[string]bool{}
	repeated := 0
	for _, l := range strings.Split(seed, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		if lines[l] {
			repeated++
		}
	}
	if len(seen) == 0 {
		return 0
	}
	return float64(repeated) / float64(len(seen))
}

// recordedFile reads the instructions file at path, model's definition named name, and reports
// whether it is one the store recorded: it decodes, its header names the definition its path
// holds, recorded records its content, and the store's checks accept it as stored
// (checkStored). It returns the definition and a held file's fallback.
func (s Store) recordedFile(path, model, name string, recorded map[string]string) (Definition, string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, "", false
	}
	d, fallback, err := decodeFile(string(data))
	if err != nil || d.Kind != KindInstructions || d.Name != name || d.Model != model {
		return Definition{}, "", false
	}
	if want := recorded[SeedKey(KindInstructions, name)]; want == "" || want != Digest(string(data)) {
		return Definition{}, "", false
	}
	if s.checkStored(d) != nil {
		return Definition{}, "", false
	}
	return d, fallback, true
}
