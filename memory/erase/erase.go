// Package erase is decision 11's erasure (docs/memory-v2.md, "Erasure and the layer purge"):
// payload indirection with a purge first. A user's erase request is a forget record
// (ledger.IsErasure) whose tombstones hold ids only; its payloads are the erased records' own
// text, and the purge replaces every copy of a payload in the files the engine owns with
// Marker, under each file's lock. What remains is the ids and Marker: the indirection is that
// nothing but an id points at the payload any more, and the payload itself is gone.
//
// Crypto-shredding is not built: backups of ~/.om are out of scope (decision 14), so there is no
// copy a key would have to reach. The harness's own transcript is outside the engine: the engine
// never writes it and cannot erase from it.
package erase

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/filelock"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/secrets"
)

// Marker is what a purged payload is replaced with.
const Marker = "[erased]"

// MinPayload is the shortest payload the purge replaces, in bytes: a shorter one (a word, a
// number) would wipe unrelated text.
const MinPayload = 12

// Forms are the byte forms a payload takes in the engine's files: as written, and as a JSON
// string body without and with encoding/json's HTML escaping. A form is listed once.
func Forms(payload string) []string {
	out := []string{payload}
	add := func(s string) {
		for _, o := range out {
			if o == s {
				return
			}
		}
		out = append(out, s)
	}
	add(jsonBody(payload, false))
	add(jsonBody(payload, true))
	return out
}

// jsonBody is s as the inside of a JSON string, escaped as encoding/json escapes it (with html,
// also <, > and &).
func jsonBody(s string, html bool) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte("0123456789abcdef"[r>>4])
			b.WriteByte("0123456789abcdef"[r&0xf])
		case html && r == '<':
			b.WriteString("\\u003c")
		case html && r == '>':
			b.WriteString("\\u003e")
		case html && r == '&':
			b.WriteString("\\u0026")
		case r == 0x2028:
			b.WriteString("\\u2028")
		case r == 0x2029:
			b.WriteString("\\u2029")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Usable is payloads without duplicates, each at least MinPayload bytes after trimming and never
// Marker itself, longest first (so a payload inside another is replaced after it).
func Usable(payloads []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range payloads {
		p = strings.TrimSpace(p)
		if len(p) < MinPayload || p == Marker || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// PurgeText is text with every form of every usable payload replaced by Marker, and how many
// replacements it made.
func PurgeText(text string, payloads []string) (string, int) {
	n := 0
	for _, p := range Usable(payloads) {
		for _, f := range Forms(p) {
			if c := strings.Count(text, f); c > 0 {
				n += c
				text = strings.ReplaceAll(text, f, Marker)
			}
		}
	}
	return text, n
}

// CountText is how many copies of the usable payloads text holds, in any form.
func CountText(text string, payloads []string) int {
	_, n := PurgeText(text, payloads)
	return n
}

// Payloads are the payloads the branch's erase requests name (ledger.IsErasure): the text of
// every record an erasure tombstones, of every level-1 record or reflection built on one, whose
// text may carry it, and their spans (Spans); text already purged is left out. The records are
// read from the branch's entries, since the fold leaves a forgotten record out.
func Payloads(branch []ledger.Entry) []string {
	type rec struct {
		id, content string
		on          []string
	}
	var recs []rec
	erased := map[string]bool{}
	for _, e := range branch {
		if data, ok := ledger.ObservationsRecordedEntry(e); ok {
			for _, o := range data.Observations {
				if ledger.IsErasure(o) {
					for _, t := range o.Forgets {
						erased[t.ID] = true
					}
					continue
				}
				recs = append(recs, rec{o.ID, o.Content, o.Replaces})
			}
		}
		if data, ok := ledger.ReflectionsRecordedEntry(e); ok {
			for _, r := range data.Reflections {
				recs = append(recs, rec{r.ID, r.Content, r.SupportingObservationIDs})
			}
		}
	}
	if len(erased) == 0 {
		return nil
	}
	var out []string
	for _, r := range recs {
		hit := erased[r.id]
		for _, id := range r.on {
			hit = hit || erased[id]
		}
		if hit && !strings.Contains(r.content, Marker) {
			out = append(out, r.content)
			out = append(out, Spans(r.content)...)
		}
	}
	return Usable(out)
}

// Spans are the runs of specific words in text, as written: a run of words each holding a digit
// or opening with a capital (the text's first word excepted, and the common sentence words
// ledger.Specifics never takes for a name), its edge punctuation trimmed. A copy of an erased
// value reworded around it (a curated memory, a level-1 merge) still holds its spans; Usable drops
// a span shorter than MinPayload.
func Spans(text string) []string {
	words := strings.Fields(text)
	var out, run []string
	flush := func() {
		if len(run) > 0 {
			out = append(out, strings.Join(run, " "))
		}
		run = nil
	}
	for i, w := range words {
		core := strings.Trim(w, ".,;:!?()[]{}\"'`")
		specific := core != "" && (strings.ContainsAny(core, "0123456789") ||
			(i > 0 && core[0] >= 'A' && core[0] <= 'Z' && !commonWords[core]))
		if !specific {
			flush()
			continue
		}
		run = append(run, core)
		if strings.ContainsAny(w[len(w)-1:], ".,;:!?") {
			flush()
		}
	}
	flush()
	return out
}

// commonWords open sentences and clauses; they never start a span.
var commonWords = map[string]bool{"I": true, "The": true, "A": true, "An": true, "This": true, "That": true, "It": true,
	"We": true, "You": true, "They": true, "He": true, "She": true, "My": true, "Our": true, "Your": true, "And": true, "But": true}

// A Surface is one named place a value can survive: the files under its roots.
type Surface struct {
	Name  string
	Roots []string
	// Lock is the lock file a write to the surface's files is made under ("" for each file's own
	// <path>.lock, when it has one).
	Lock string
	// Layer is set for the curated store's layer, whose files a purge rewrites through the store
	// (layer.Store.Scrub) so its manifest and held index stay true.
	Layer *layer.Store
}

// Surfaces are the engine's own files under home (~/.om for om, PiG's config root for the
// adapter): the curated tree (memories, their usage ledger and index, the layer, the core log,
// the per-model usage ledgers) always; with sessions, om's session stores (records, summaries'
// details, value and evidence records, sleep entries), its recorded turns and the sleep pass's
// checkpoints, and the image's own layer (layerRoot, "" for none). A harness's own session files
// are never a surface.
func Surfaces(home string, sessions bool, layerRoot string) []Surface {
	memories := curated.DefaultDir(home)
	lay := curated.New(memories).Layer()
	out := []Surface{
		{Name: "core log", Roots: []string{filepath.Join(memories, "core")}, Lock: filepath.Join(memories, "core", ".lock")},
		{Name: "layer", Roots: []string{filepath.Join(memories, "harness")}, Layer: &lay},
		{Name: "curated memory", Roots: []string{memories}, Lock: filepath.Join(memories, ".lock")},
	}
	if sessions {
		out = append(out,
			Surface{Name: "records and summaries", Roots: []string{filepath.Join(home, "sessions")}},
			Surface{Name: "recorded turns", Roots: []string{filepath.Join(home, "transcripts")}},
			Surface{Name: "checkpoints", Roots: []string{filepath.Join(home, "sleep")}, Lock: filepath.Join(home, "sleep", ".lock")},
		)
		if layerRoot != "" {
			out = append(out, Surface{Name: "image layer", Roots: []string{layerRoot}})
		}
	}
	return out
}

// Residue is one surface's count of payload copies (or, for a secret scan, of secret values) and
// the files holding them.
type Residue struct {
	Surface string `json:"surface"`
	Files   int    `json:"files"`
	Copies  int    `json:"copies"`
}

// files lists s's regular files, each once across surfaces (claimed): a nested root's files
// belong to the first surface that lists them. Lock files and temp files are skipped.
func files(s Surface, claimed map[string]bool) []string {
	var out []string
	for _, root := range s.Roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			base := d.Name()
			if !d.Type().IsRegular() || strings.HasSuffix(base, ".lock") || base == ".lock" || strings.Contains(base, ".tmp") {
				return nil
			}
			if claimed[path] {
				return nil
			}
			claimed[path] = true
			out = append(out, path)
			return nil
		})
	}
	sort.Strings(out)
	return out
}

// Scan counts the payload copies on every surface.
func Scan(surfaces []Surface, payloads []string) []Residue {
	return walk(surfaces, func(text string) int { return CountText(text, payloads) })
}

// ScanSecrets counts the secret values on every surface.
func ScanSecrets(surfaces []Surface) []Residue {
	return walk(surfaces, func(text string) int { _, f := secrets.Scan(text); return len(f) })
}

func walk(surfaces []Surface, count func(string) int) []Residue {
	claimed := map[string]bool{}
	var out []Residue
	for _, s := range surfaces {
		r := Residue{Surface: s.Name}
		for _, path := range files(s, claimed) {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if n := count(string(data)); n > 0 {
				r.Files++
				r.Copies += n
			}
		}
		out = append(out, r)
	}
	return out
}

// Purge replaces every payload copy on every surface with Marker, each file rewritten whole
// (temp file and rename) under its surface's lock, and returns what it replaced.
func Purge(surfaces []Surface, payloads []string) ([]Residue, error) {
	payloads = Usable(payloads)
	if len(payloads) == 0 {
		return nil, nil
	}
	return rewrite(surfaces, func(text string) (string, int) { return PurgeText(text, payloads) },
		func(text string) bool { return CountText(text, payloads) > 0 }, func(text string) string { t, _ := PurgeText(text, payloads); return t })
}

// Session is the writer a purge's layer generations name.
const Session = "om-erase"

// PurgeSecrets redacts every secret value on every surface outside the layer (a JSON line's
// string values with secrets.RedactJSON, any other file with secrets.Redact), keeping the
// variable name; the layer goes through its store (layer.Store.Scrub) so its manifest stays true.
func PurgeSecrets(surfaces []Surface) ([]Residue, error) {
	return rewrite(surfaces, func(text string) (string, int) {
		_, found := secrets.Scan(text)
		if len(found) == 0 {
			return text, 0
		}
		if json.Valid([]byte(strings.SplitN(text, "\n", 2)[0])) {
			return redactLines(text), len(found)
		}
		return secrets.Redact(text), len(found)
	}, secrets.Contains, secrets.Redact)
}

// redactLines is secrets.RedactJSON over each line of a JSON-lines text.
func redactLines(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			lines[i] = secrets.RedactJSON(l)
		}
	}
	return strings.Join(lines, "\n")
}

func rewrite(surfaces []Surface, fix func(string) (string, int), match func(string) bool, fixLayer func(string) string) ([]Residue, error) {
	claimed := map[string]bool{}
	var out []Residue
	var errs []error
	for _, s := range surfaces {
		r := Residue{Surface: s.Name}
		if s.Layer != nil {
			files(s, claimed)
			rep, err := s.Layer.Scrub(match, fixLayer, Session)
			if err != nil {
				errs = append(errs, err)
			}
			r.Files = rep.Files()
			out = append(out, r)
			continue
		}
		run := func(fn func() error) error {
			if s.Lock == "" {
				return fn()
			}
			if err := os.MkdirAll(filepath.Dir(s.Lock), 0o700); err != nil {
				return err
			}
			return filelock.WithLock(s.Lock, fn)
		}
		err := run(func() error {
			for _, path := range files(s, claimed) {
				one := func() error {
					data, err := os.ReadFile(path)
					if err != nil {
						return nil
					}
					text, n := fix(string(data))
					if n == 0 || text == string(data) {
						return nil
					}
					r.Files++
					r.Copies += n
					return writeAtomic(path, text)
				}
				var err error
				if _, statErr := os.Stat(path + ".lock"); s.Lock == "" && statErr == nil {
					err = filelock.WithLock(path+".lock", one)
				} else {
					err = one()
				}
				if err != nil {
					errs = append(errs, err)
				}
			}
			return nil
		})
		if err != nil {
			errs = append(errs, err)
		}
		out = append(out, r)
	}
	return out, errors.Join(errs...)
}

// writeAtomic writes text to path through a temp file beside it and one rename.
func writeAtomic(path, text string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Total is the copies over every surface.
func Total(rs []Residue) int {
	n := 0
	for _, r := range rs {
		n += r.Copies
	}
	return n
}
