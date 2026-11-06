package memeval

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/drvova/pig-observational-memory/memory/codepolicy"
	"github.com/drvova/pig-observational-memory/memory/omhost"
	"github.com/drvova/pig-observational-memory/memory/omtool"
	"github.com/drvova/pig-observational-memory/memory/sandbox"
	"github.com/drvova/pig-observational-memory/memory/session"
)

// ChildLister runs snippets as memory tool code in a real namespace child (sandbox), as the
// memory tool would: the binary re-executes itself, so it must link this package (which links
// sandbox, whose init catches the child marker).
type ChildLister struct {
	once sync.Once
	sup  *sandbox.Supervisor
}

// List runs snippet over branch and returns the ids of the rows it lists.
func (l *ChildLister) List(branch []session.Entry, snippet string) ([]string, error) {
	l.once.Do(func() {
		l.sup = sandbox.New(sandbox.Config{Mode: sandbox.ModeNamespace, Methods: sandbox.NamespaceMethods()})
	})
	code := "l := " + snippet + "\nfor _, id := range l.IDs() { fmt.Println(id) }"
	p, err := codepolicy.Check(codepolicy.Input{Code: code})
	if err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	call := &omhost.Call{Entries: branch, Skip: []string{omtool.ToolName}}
	res := l.sup.Run(context.Background(), sandbox.Request{Program: p}, call)
	if res.Outcome == sandbox.OutcomeCrashed && strings.Contains(res.Err, "signal: killed") {
		// The child is the out-of-memory killer's first pick; the next call starts a fresh one.
		res = l.sup.Run(context.Background(), sandbox.Request{Program: p}, call)
	}
	if res.Outcome != sandbox.OutcomeOK {
		return nil, fmt.Errorf("%s: %s", res.Outcome, res.Err)
	}
	if res.Cut > 0 {
		return nil, fmt.Errorf("output cut by %d characters", res.Cut)
	}
	return strings.Fields(res.Out), nil
}

// Close stops the child.
func (l *ChildLister) Close() {
	if l.sup != nil {
		l.sup.Kill()
	}
}

// CachingLister answers a snippet over a branch it has already run from its cache: the
// controls evaluate one run several times. The key is the snippet and the branch's length and
// tip (the compaction entry, with its summary), which identify a simulated branch.
type CachingLister struct {
	Lister Lister
	mu     sync.Mutex
	cache  map[string][]string
}

// List is Lister.List, cached.
func (c *CachingLister) List(branch []session.Entry, snippet string) ([]string, error) {
	key := fmt.Sprint(len(branch), "\x00", snippet)
	if len(branch) > 0 {
		tip := branch[len(branch)-1]
		key += "\x00" + tip.ID + "\x00" + fmt.Sprint(tip.Summary)
	}
	c.mu.Lock()
	ids, ok := c.cache[key]
	c.mu.Unlock()
	if ok {
		return ids, nil
	}
	ids, err := c.Lister.List(branch, snippet)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.cache == nil {
		c.cache = map[string][]string{}
	}
	c.cache[key] = ids
	c.mu.Unlock()
	return ids, nil
}
