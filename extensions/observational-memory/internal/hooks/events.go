// Package hooks implements the extension's event handlers: level-0 extraction at turn ends,
// the compaction hook (level 0 and the one level-1 request), the auto-compaction-off trigger
// and request-context thinning (docs/memory-v2.md).
package hooks

import (
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
)

// Now is the clock; tests replace it.
var Now = time.Now

// OnAgentStart handles agent_start: it loads the session's configuration, which the context
// subscription follows (the extension makes no request and starts no work here).
func OnAgentStart(rt *runtime.Runtime, h host.Host) {
	rt.EnsureConfig(h)
}

// OnTurnEnd handles turn_end: level-0 extraction, which makes no model request
// (extractTurnV2).
func OnTurnEnd(rt *runtime.Runtime, h host.Host) {
	rt.EnsureConfig(h)
	extractTurnV2(rt, h)
}

// OnAgentSettledEvent is the agent_settled handler: the auto-compaction-off trigger
// (MaybeTriggerCompaction).
func OnAgentSettledEvent(rt *runtime.Runtime, h host.Host) {
	MaybeTriggerCompaction(rt, h)
}

func sessionIdentityOf(h host.Host) string {
	if id := h.SessionID(); id != "" {
		return id
	}
	return h.SessionFile()
}

func branchLeafID(entries []ledger.Entry) string {
	if len(entries) == 0 {
		return ""
	}
	return entries[len(entries)-1].ID
}

// notifyUI shows a notification. It never panics: a notification is never worth taking the
// extension process down, whatever state the host's session is in.
func notifyUI(h host.Host, message, level string) {
	defer func() { _ = recover() }()
	if h.HasUI() {
		h.Notify(message, level)
	}
}

// sessionHeadersFor mirrors Pi's own session headers on the level-1 request to Console Go
// (opencode.ai), which rejects requests without x-opencode-session.
func sessionHeadersFor(h host.Host, resolved runtime.ResolveResult) runtime.ResolveResult {
	provider := host.ModelProvider(resolved.Model)
	baseURL := host.ModelString(resolved.Model, "baseUrl")
	if provider != "opencode" && provider != "opencode-go" && !strings.Contains(baseURL, "opencode.ai") {
		return resolved
	}
	sessionID := h.SessionID()
	if sessionID == "" {
		return resolved
	}
	headers := make(map[string]any, len(resolved.Headers)+2)
	for k, v := range resolved.Headers {
		headers[k] = v
	}
	headers["x-opencode-session"] = sessionID
	headers["x-opencode-client"] = "pi"
	resolved.Headers = headers
	return resolved
}
