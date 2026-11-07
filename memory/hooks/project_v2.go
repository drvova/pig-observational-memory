package hooks

import (
	"context"

	"github.com/drvova/pig-observational-memory/memory/debuglog"
	"github.com/drvova/pig-observational-memory/memory/extract"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/project"
	"github.com/drvova/pig-observational-memory/memory/runtime"
)

// Project knowledge (docs/brain-memory.md, decision 20; memory/project): the HEAD structure view
// the adapter answers (host.StructureReader) is asked about the project records, never stored.

// repoOfV2 is the session's repository, read once per compaction (Runtime.CachedRepo, keyed as
// encodingOf keys it).
func repoOfV2(rt *runtime.Runtime, h host.Host, entries []ledger.Entry) (host.Repo, bool) {
	key := h.Cwd() + "\x00"
	if i := ledger.FindLastCompactionIndex(entries); i >= 0 {
		key += entries[i].ID
	}
	return rt.CachedRepo(key, h)
}

// stampV2 sets the new project records' body hashes to their paths' blobs at HEAD
// (project.Stamp), when the host answers the view; a host that reads none, or a failed answer,
// leaves them without one.
func stampV2(h host.Host, result *extract.Result) {
	reader, ok := h.(host.StructureReader)
	if !ok {
		return
	}
	q := project.StampQuery(result.Observations())
	if project.Empty(q) {
		return
	}
	answer, ok, err := reader.Structure(q)
	if err != nil || !ok {
		return
	}
	for i := range result.Batches {
		project.Stamp(result.Batches[i].Observations, answer)
	}
}

// freshness is a compaction's view of its project records: the repository, the verdicts, and
// the diffs of the changed ones once asked (diffsV2).
type freshness struct {
	repo     host.Repo
	verdicts []project.Verdict
	reader   host.StructureReader
}

// freshnessV2 judges the active project records of entries against HEAD (project.Verdicts),
// none when the host answers no view, the session is in no repository, or no project record
// names a path or symbol.
func freshnessV2(ctx context.Context, rt *runtime.Runtime, h host.Host, entries []ledger.Entry) freshness {
	reader, ok := h.(host.StructureReader)
	if !ok {
		return freshness{}
	}
	repo, ok := repoOfV2(rt, h, entries)
	if !ok || repo.ID == "" {
		return freshness{}
	}
	records := ledger.FoldLedger(entries, "").ActiveObservations
	q := project.Query(records, repo)
	if project.Empty(q) {
		return freshness{}
	}
	answer, ok, err := reader.Structure(q)
	if err != nil || !ok {
		data := map[string]any{"asked": len(q.Paths) + len(q.Symbols) + len(q.Blobs)}
		if err != nil {
			data["errorMessage"] = err.Error()
		}
		debuglog.Log(ctx, "compaction.v2.freshness", data)
		return freshness{}
	}
	out := freshness{repo: repo, verdicts: project.Verdicts(records, repo, answer), reader: reader}
	gone := 0
	for _, v := range out.verdicts {
		if v.Kind == project.Gone {
			gone++
		}
	}
	debuglog.Log(ctx, "compaction.v2.freshness", map[string]any{"asked": len(q.Paths) + len(q.Symbols) + len(q.Blobs),
		"head": answer.Head, "gone": gone, "changed": len(out.verdicts) - gone})
	return out
}

// texts are the verdicts' texts by record id (selection.Input.Freshness).
func (f freshness) texts() map[string]string { return project.Texts(f.verdicts) }
