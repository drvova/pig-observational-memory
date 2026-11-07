package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/erase"
	"github.com/drvova/pig-observational-memory/memory/hooks"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/neutral"
	"github.com/drvova/pig-observational-memory/memory/secrets"
)

// surfaces are the engine's files under root (erase.Surfaces): om's session stores are its own.
func surfaces(root string) []erase.Surface {
	return erase.Surfaces(root, true, filepath.Join(root, "layer"))
}

// residueLines prints one line per surface.
func residueLines(out io.Writer, what string, rs []erase.Residue) error {
	for _, r := range rs {
		if _, err := fmt.Fprintf(out, "  %-22s %d %s in %d files\n", r.Surface+":", r.Copies, what, r.Files); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out, "The harness's own transcript is outside the engine: om never writes it and cannot erase from it.")
	return err
}

// runErase is om erase (docs/memory-v2.md, "Erasure and the layer purge"). With no text it purges
// the payloads of every session's erase requests (a user's "erase … from memory", recorded by
// level 0) that no purge has handled, and marks each session's requests erased; with --text the
// words the user typed at the command line are the payload. --check only counts what is left,
// surface by surface, and changes nothing.
func runErase(root string, text []string, check bool, out io.Writer) error {
	var payloads []string
	pending := map[string][]string{}
	hosts := map[string]*neutral.Host{}
	for _, path := range storeFiles(root) {
		rel, _ := filepath.Rel(filepath.Join(root, "sessions"), path)
		key := filepath.ToSlash(rel)
		h := &neutral.Host{Session: key, Root: root, Store: neutral.Store{Path: path}, HarnessName: filepath.Base(filepath.Dir(path))}
		branch, err := h.GetBranch()
		if err != nil {
			continue
		}
		folded := ledger.FoldLedger(branch, "")
		payloads = append(payloads, erase.Payloads(branch)...)
		if ids := hooks.PendingErasures(branch, folded); len(ids) > 0 {
			pending[key], hosts[key] = ids, h
		}
	}
	if t := strings.TrimSpace(strings.Join(text, " ")); t != "" {
		payloads = append(payloads, t)
	}
	payloads = erase.Usable(payloads)
	if check || len(payloads) == 0 {
		if _, err := fmt.Fprintf(out, "om erase --check: %d payloads to look for\n", len(payloads)); err != nil {
			return err
		}
		return residueLines(out, "copies", erase.Scan(surfaces(root), payloads))
	}
	rs, err := erase.Purge(surfaces(root), payloads)
	if err != nil {
		return err
	}
	for key, ids := range pending {
		if err := hosts[key].AppendEntry(hooks.OMErased, hooks.ErasedData{Erasures: ids, Residue: rs}); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(out, "om erase: %d payloads purged; replaced per surface:\n", len(payloads)); err != nil {
		return err
	}
	if err := residueLines(out, "copies", rs); err != nil {
		return err
	}
	if left := erase.Total(erase.Scan(surfaces(root), payloads)); left > 0 {
		return fmt.Errorf("om erase: %d copies remain after the purge", left)
	}
	return nil
}

// runPurge is om purge: the secret purge of decision 10 over files written before writes were
// scanned. The layer goes through its store (a prose definition redacted, a code one forgotten,
// a blob holding a value removed), every other engine file is redacted in place; both under the
// stores' locks. --check only counts.
func runPurge(root string, check bool, out io.Writer) error {
	if check {
		if _, err := fmt.Fprintln(out, "om purge --check: secret values left"); err != nil {
			return err
		}
		return residueLines(out, "values", erase.ScanSecrets(surfaces(root)))
	}
	rs, err := PurgeSecrets(root)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "om purge: secret values redacted per surface"); err != nil {
		return err
	}
	return residueLines(out, "values", rs)
}

// PurgeSecrets is the secret purge over root's engine files (erase.PurgeSecrets); om sleep runs it
// too, under the sleep pass's lock.
func PurgeSecrets(root string) ([]erase.Residue, error) {
	return erase.PurgeSecrets(surfaces(root))
}

// layerSecretLine is om sleep's report of the layer purge, "" when nothing held a value.
func layerSecretLine(root string) (string, error) {
	store := curated.New(curated.DefaultDir(root)).Layer()
	rep, err := store.Scrub(secrets.Contains, secrets.Redact, erase.Session)
	if err != nil || rep.Files() == 0 {
		return "", err
	}
	return fmt.Sprintf("layer: %d definitions redacted, %d code definitions holding a secret forgotten, %d blobs removed", rep.Rewritten, rep.Forgotten, rep.Blobs), nil
}
