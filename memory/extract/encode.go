package extract

import (
	"path/filepath"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/project"
	"github.com/drvova/pig-observational-memory/memory/secrets"
)

// encode sets every record's encoding fields (contracts/encoding.md) that its extractor left
// empty: its source from the role of its first source entry, the authority that source carries,
// the hedge of its own sentence, its time hint, the name@version keys of its text, the writer
// and the repository frame. It runs after the ids are assigned and none of these fields enter an
// id, so a record keeps the id an engine without them gives it.
func (x *extraction) encode() {
	for _, r := range x.records {
		if r.obs.Episode == "" && r.anchor >= 0 && r.anchor < len(x.episodes) {
			r.obs.Episode = x.episodes[r.anchor]
		}
		// A plan's requirement is the request it was made under (ledger.Observation.DerivedFrom).
		if r.obs.Status == ledger.StatusProposed && r.obs.DerivedFrom == "" && r.anchor >= 0 && r.anchor < len(x.requests) && x.requests[r.anchor] >= 0 {
			r.obs.DerivedFrom = x.in.Entries[x.requests[r.anchor]].ID
		}
		Encode(&r.obs, x.sourceOf(r), x.in.Writer, x.in.Frame, x.in.RepoRoot)
	}
}

// Encode fills o's empty encoding fields: source (unless o has one), authority from the source,
// hedge, time hint, the versions its text names, writer and frame (the repository's, with the
// record's one path made relative to root, and a project record's one symbol). An extractor that knows better sets a field before;
// Encode never overwrites one.
func Encode(o *ledger.Observation, source string, writer ledger.Writer, frame ledger.Frame, root string) {
	if o.Source == "" {
		o.Source = source
	}
	if o.Authority == "" {
		o.Authority = ledger.AuthorityOf(o.Source)
	}
	if o.Hedge == "" {
		o.Hedge = hedgeOf(*o)
	}
	if o.TimeHint == "" {
		o.TimeHint = ledger.TimeHintOf(*o)
	}
	for _, v := range ledger.VersionsIn(o.Content) {
		o.Keys.Add(ledger.KeyVersions, v)
	}
	if o.Writer == (ledger.Writer{}) {
		o.Writer = writer
	}
	if o.Frame == (ledger.Frame{}) && frame.RepoID != "" {
		o.Frame = ledger.Frame{RepoID: frame.RepoID, Branch: frame.Branch, Head: frame.Head}
		if paths := o.Keys.Paths; len(paths) == 1 {
			o.Frame.Path = repoPath(paths[0], root)
		}
		// A project record names its one symbol (docs/brain-memory.md, decision 20), which the
		// HEAD structure view checks it against.
		o.Frame.Symbol = project.Symbol(*o)
	}
}

// hedgeOf is a record's hedge: a file or commit record is a label code wrote, so stated; an
// attempt's is that of the model's words it carries; any other record's that of its text.
func hedgeOf(o ledger.Observation) string {
	switch o.Kind {
	case ledger.KindFile, ledger.KindCommit:
		return ledger.HedgeStated
	case ledger.KindAttempt:
		return ledger.HedgeOf(attemptWords(o))
	}
	return ledger.HedgeOf(o.Content)
}

// repoPath is path relative to the repository root when inside it, else path.
func repoPath(path, root string) string {
	if root == "" || !filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return path
	}
	return rel
}

// sourceOf is the source code gives a record (contracts/encoding.md): an attempt, file or commit
// record is what a tool showed; a memory tool note and a reply are the session model's; any
// other record takes the role of its first source entry.
func (x *extraction) sourceOf(r *record) string {
	switch r.obs.Kind {
	case ledger.KindAttempt, ledger.KindFile, ledger.KindCommit:
		return ledger.SourceToolObserved
	case ledger.KindNote:
		return ledger.SourceAssistantClaim
	}
	if r.first < 0 || r.first >= len(x.in.Entries) {
		return ""
	}
	return EntrySource(x.in.Entries[r.first])
}

// EntrySource is the source of a record taken from entry: a user message is the user's words, an
// assistant message the model's claim, a tool result or a user's shell command what a tool
// showed, a custom message by its origin (ledger.SourceOfCustomType, external-untrusted for any
// other custom message); "" for an entry that is none of these.
func EntrySource(entry ledger.Entry) string {
	if entry.Type == "custom_message" {
		if s := ledger.SourceOfCustomType(entry.CustomType); s != "" {
			return s
		}
		return ledger.SourceExternalUntrusted
	}
	if entry.Type != "message" || entry.Message == nil {
		return ""
	}
	switch entry.Message["role"] {
	case "user":
		return ledger.SourceUserSaid
	case "assistant":
		return ledger.SourceAssistantClaim
	case "toolResult", "bashExecution":
		return ledger.SourceToolObserved
	}
	return ""
}

// redactKeys is keys with every secret redacted (secrets.Redact), as a record's text is.
func redactKeys(k ledger.Keys) ledger.Keys {
	if k.IsZero() {
		return k
	}
	out := ledger.Keys{}
	for _, kind := range ledger.KeyKinds {
		for _, v := range k.Get(kind) {
			out.Add(kind, secrets.Redact(v))
		}
	}
	return out
}
