package hooks

import (
	"context"
	"fmt"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/thin"
)

// OnContext is the context event handler (PiG emits it before each provider request): under
// memory v2 it thins the aged tool results of the request's context in place (thin.Compute,
// docs/memory-v2.md, "Thinning aged tool results") and returns how many it shortened. v1,
// passive mode, a branch it cannot read and a panic leave the messages untouched. It makes no
// model request.
func OnContext(rt *runtime.Runtime, h host.Host, messages []host.ContextMessage) (thinned int) {
	defer func() {
		if recovered := recover(); recovered != nil {
			thinned = 0
			debuglog.Log(context.Background(), "thin.v2.recovered", map[string]any{"panic": fmt.Sprint(recovered)})
		}
	}()
	rt.EnsureConfig(h)
	if cfg := rt.Config(); !cfg.V2() || cfg.Passive || len(messages) == 0 {
		return 0
	}
	entries, err := h.GetBranch()
	if err != nil {
		return 0
	}
	c := ledger.FindLastCompactionIndex(entries)
	if c < 0 {
		return 0
	}
	// The plan is derived once per compaction, from the branch and settings as they stand at
	// the first request after it, and reused until the next compaction: a reserveTokens edit,
	// a defined tool registered or forgotten, or a result landing later changes no request's
	// bytes between two compactions.
	key := sessionIdentityOf(h) + "\x00" + entries[c].ID
	plan, cached := rt.CachedThinPlan(key)
	if !cached {
		// PiG's reserveTokens as its settings hold it now.
		cfg := rt.LiveConfig(h.Cwd())
		model := h.SessionModel()
		plan = thin.Compute(thin.Input{
			Entries:       entries[:c+1],
			ContextWindow: host.ModelContextWindow(model),
			ReserveTokens: cfg.PiCompaction.ReserveTokensFor(host.ModelProvider(model), host.ModelID(model)),
			SkipTools:     harness.MemoryToolNames(rt),
			Cwd:           h.Cwd(),
		})
		rt.SetThinPlan(key, plan)
	}
	if len(plan) == 0 {
		return 0
	}
	thinned = thin.Apply(messages, plan)
	if thinned > 0 {
		debuglog.Log(debuglog.With(context.Background(), debugScope(rt, h)), "thin.v2", map[string]any{"thinned": thinned, "planned": len(plan)})
	}
	return thinned
}
