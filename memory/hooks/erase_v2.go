package hooks

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/debuglog"
	"github.com/drvova/pig-observational-memory/memory/erase"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/runtime"
)

// OMErased marks the erase requests whose payloads a purge removed (docs/memory-v2.md, "Erasure
// and the layer purge"): their record ids and the copies replaced per surface, never a payload.
const OMErased = "om.erased"

// EngineSessions is a host whose session files are the engine's own (the neutral store under
// ~/.om), so an erasure purges them too; a harness's session files never are.
type EngineSessions interface {
	EngineOwnsSessions() bool
}

// ErasedData is an om.erased entry's data.
type ErasedData struct {
	Erasures []string        `json:"erasures"`
	Residue  []erase.Residue `json:"residue"`
}

// PendingErasures are the erase requests on branch (ledger.IsErasure) no om.erased entry names.
func PendingErasures(branch []ledger.Entry, folded ledger.FoldedLedger) []string {
	done := map[string]bool{}
	for _, e := range branch {
		if e.Type != "custom" || e.CustomType != OMErased {
			continue
		}
		if data, ok := e.Data.(map[string]any); ok {
			if ids, ok := data["erasures"].([]any); ok {
				for _, id := range ids {
					if s, ok := id.(string); ok {
						done[s] = true
					}
				}
			}
		}
	}
	var out []string
	for _, o := range folded.Observations {
		if ledger.IsErasure(o) && !done[o.ID] {
			out = append(out, o.ID)
		}
	}
	return out
}

// ErasePending purges the payloads of the branch's erase requests no purge has handled yet from
// every file the engine owns (erase.Surfaces), under each surface's lock, and appends one
// om.erased entry naming them. It runs after level 0 at a turn end, whichever engine ran level 0:
// the Go client runs it after the image's turn end too. It makes no model request and never fails
// a turn. rt may be nil (a client of the engine process, which has no runtime).
func ErasePending(rt *runtime.Runtime, h host.Host) {
	ctx := context.Background()
	if rt != nil {
		ctx = debuglog.With(ctx, debugScope(rt, h))
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			debuglog.Log(ctx, "erase.recovered", map[string]any{"panic": recovered})
		}
	}()
	homer, ok := h.(host.ConfigHomer)
	if !ok || homer.ConfigHome() == "" {
		return
	}
	branch, err := h.GetBranch()
	if err != nil {
		return
	}
	if !holdsErasure(branch) {
		return
	}
	folded := ledger.FoldLedger(branch, "")
	ids := PendingErasures(branch, folded)
	if len(ids) == 0 {
		return
	}
	residue, err := ErasePayloads(homer.ConfigHome(), h, erase.Payloads(branch))
	debuglog.Log(ctx, "erase.purged", map[string]any{"erasures": ids, "copies": erase.Total(residue), "error": errString(err)})
	if err != nil {
		return
	}
	_ = appendEntry(h, OMErased, ErasedData{Erasures: ids, Residue: residue})
}

// ErasePayloads purges payloads from the engine's files under home: the session stores, recorded
// turns, checkpoints and the image's layer too when h's sessions are the engine's own.
func ErasePayloads(home string, h host.Host, payloads []string) ([]erase.Residue, error) {
	sessions := false
	if es, ok := h.(EngineSessions); ok {
		sessions = es.EngineOwnsSessions()
	}
	layerRoot := ""
	if sessions {
		layerRoot = filepath.Join(home, "layer")
	}
	residue, err := erase.Purge(erase.Surfaces(home, sessions, layerRoot), payloads)
	if residue == nil {
		residue = []erase.Residue{}
	}
	return residue, err
}

// holdsErasure is the cheap check before the fold: some recorded batch on branch holds a record
// whose text opens with ledger.ErasurePrefix.
func holdsErasure(branch []ledger.Entry) bool {
	for _, e := range branch {
		if e.Type != "custom" || e.CustomType != ledger.OMObservationsRecorded {
			continue
		}
		data, _ := e.Data.(map[string]any)
		list, _ := data["observations"].([]any)
		for _, o := range list {
			if m, ok := o.(map[string]any); ok {
				if c, _ := m["content"].(string); strings.HasPrefix(c, ledger.ErasurePrefix) {
					return true
				}
			}
		}
	}
	return false
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
