package extract

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/secrets"
	"github.com/drvova/pig-observational-memory/memory/serialize"
)

// Revisions (docs/memory-v2.md, "Revisions"): each successful file write or edit level 0 reads
// becomes one small revision of its file: the diff's size and first changed line, redacted, and a
// hash chain over the file's revisions on the branch (Prev, the file's previous revision's id),
// linked to the standing records about the file. They are code only and stored in their own
// om.revisions.recorded entry, beside the extraction's batches, never as records the summary
// shows. The image writes them byte for byte the same (lisp/src/extract.lisp).

// RevisionLinks bounds a revision's links.
const RevisionLinks = 5

// revisionArgs are the arguments that hold a write's or an edit's new text, and an edit's old.
var (
	revisionNewArgs = []string{"new_string", "content", "file_text", "new_str", "newText", "new_source"}
	revisionOldArgs = []string{"old_string", "old_str", "oldText"}
)

func firstString(args map[string]any, names []string) string {
	for _, n := range names {
		if s, ok := args[n].(string); ok {
			return s
		}
	}
	return ""
}

// lineCount is how many lines text holds: 0 for none, else its newlines plus one for a last line
// without one.
func lineCount(text string) int {
	if text == "" {
		return 0
	}
	n := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		n++
	}
	return n
}

// revisionTexts are a call's new and old texts: one pair, or a multi-edit's edits joined.
func revisionTexts(args map[string]any) (newText, oldText string) {
	if edits, ok := args["edits"].([]any); ok {
		var news, olds []string
		for _, e := range edits {
			if m, ok := e.(map[string]any); ok {
				news = append(news, firstString(m, revisionNewArgs))
				olds = append(olds, firstString(m, revisionOldArgs))
			}
		}
		return strings.Join(news, "\n"), strings.Join(olds, "\n")
	}
	return firstString(args, revisionNewArgs), firstString(args, revisionOldArgs)
}

// revisionSummary is the first non-blank line of the new text not among the old text's lines
// (else its first non-blank line), redacted and clipped.
func revisionSummary(newText, oldText string) string {
	old := map[string]bool{}
	for _, l := range strings.Split(oldText, "\n") {
		old[strings.TrimSpace(l)] = true
	}
	first := ""
	for _, l := range strings.Split(newText, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if first == "" {
			first = l
		}
		if !old[l] {
			first = l
			break
		}
	}
	return clip(secrets.Redact(first), serialize.BriefKeyArgRunes)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// RevisionID is a revision's id: the first 12 hex characters of the SHA-256 of
// "revision\0<prev>\0<path>\0<digest>\0<added>\0<removed>", digest being the SHA-256 of the
// redacted new text, so each id commits to the file's chain before it.
func RevisionID(prev, path, digest string, added, removed int) string {
	return sha256Hex("revision\x00" + prev + "\x00" + path + "\x00" + digest + "\x00" + strconv.Itoa(added) + "\x00" + strconv.Itoa(removed))[:12]
}

// revisionChain is each path's newest revision id on entries.
func revisionChain(entries []ledger.Entry) map[string]string {
	chain := map[string]string{}
	for _, e := range entries {
		if data, ok := ledger.RevisionsRecordedEntry(e); ok {
			for _, r := range data.Revisions {
				chain[r.Path] = r.ID
			}
		}
	}
	return chain
}

// revision adds the revision of a successful write or edit of p at entry i.
func (x *extraction) revision(i int, call toolCall, p string) {
	if x.chain == nil {
		x.chain = revisionChain(x.in.Entries[:x.in.End])
	}
	newText, oldText := revisionTexts(call.args)
	key := x.pathKey(p)
	r := ledger.Revision{Path: key, Entry: x.in.Entries[i].ID, Prev: x.chain[key], Added: lineCount(newText), Removed: lineCount(oldText), Summary: revisionSummary(newText, oldText)}
	r.ID = RevisionID(r.Prev, r.Path, sha256Hex(secrets.Redact(newText)), r.Added, r.Removed)
	display := x.pathDisplay(p)
	for _, o := range x.in.Ledger.ActiveObservations {
		if len(r.Links) == RevisionLinks {
			break
		}
		switch o.Kind {
		case ledger.KindDecision, ledger.KindConvention, ledger.KindFile:
		default:
			continue
		}
		if o.Kind == ledger.KindFile && o.Target == key {
			r.Links = append(r.Links, o.ID)
			continue
		}
		for _, k := range o.Keys.Paths {
			if k == key || k == display {
				r.Links = append(r.Links, o.ID)
				break
			}
		}
	}
	x.chain[key] = r.ID
	x.revisions = append(x.revisions, r)
}
