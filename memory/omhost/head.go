package omhost

import (
	"context"
	"errors"
	"strconv"

	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/om"
	"github.com/drvova/pig-observational-memory/memory/om/omwire"
	"github.com/drvova/pig-observational-memory/memory/project"
)

// The HEAD structure view on read (docs/brain-memory.md, decision 20): om.Head pages HEAD's
// files, and the notes reads print each project record's freshness verdict. Both ask the host
// (Call.Structure) at call time; nothing of the view is stored.

// errNoView is om.Head's error when the call has no repository view.
var errNoView = errors.New("this session is in no git repository the host reads (the HEAD structure view is unavailable)")

// head answers om.Head.
func (c *Call) head(ctx context.Context, prefix string, o omwire.Options) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.Structure == nil {
		return nil, errNoView
	}
	limit := 0
	if o.Limit != nil {
		limit = *o.Limit
	}
	offset := 0
	if o.Offset != nil {
		offset = *o.Offset
	}
	if limit < 0 || offset < 0 {
		return nil, errors.New("limit and offset must not be negative")
	}
	if limit == 0 {
		limit = HeadPage
	}
	limit = min(limit, MaxHeadPage)
	a, ok, err := c.Structure.Structure(host.StructureQuery{List: &host.ListQuery{Prefix: prefix, Offset: offset, Limit: limit}})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errNoView
	}
	v := om.HeadView{RepoID: a.RepoID, Head: a.Head, Prefix: prefix, Files: a.Files, Offset: offset, Total: a.Total, Truncated: a.Truncated}
	if v.Files == nil {
		v.Files = []string{}
	}
	if end := offset + len(a.Files); len(a.Files) > 0 && end < a.Total {
		v.Next = "om.Head(" + strconv.Quote(prefix) + ", om.Offset(" + strconv.Itoa(end) + "), om.Limit(" + strconv.Itoa(limit) + "))"
	}
	return v, nil
}

// HeadPage and MaxHeadPage are om.Head's default and largest page.
const (
	HeadPage    = 50
	MaxHeadPage = 500
)

// freshness is each active project record's verdict text, asked of the host at each notes read
// that shows rows (the host caches its view per HEAD).
func (c *Call) freshness() map[string]string {
	if c.Structure == nil || c.Repo == nil {
		return nil
	}
	repo, ok := c.Repo()
	if !ok || repo.ID == "" {
		return nil
	}
	records := ledger.FoldLedger(c.Entries, "").ActiveObservations
	q := project.Query(records, repo)
	if project.Empty(q) {
		return nil
	}
	a, ok, err := c.Structure.Structure(q)
	if err != nil || !ok {
		return nil
	}
	return project.Texts(project.Verdicts(records, repo, a))
}
