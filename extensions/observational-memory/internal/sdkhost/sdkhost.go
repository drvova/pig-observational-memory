// Package sdkhost adapts the PiG SDK's extension Context to the host
// interfaces the memory logic uses.
package sdkhost

import (
	"context"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
)

// Host is a host.Host over one sdk.Context. A handler may retain it past its
// own completion (background memory workers do): later host calls use the
// live extension connection.
//
// The SDK panics from Cwd, HasUI, Model and ModelProvider once the session
// has been replaced or reloaded. A retained host must never take the
// extension process down that way, so the values that cannot change within a
// session (working directory, UI) are read once while the handler's context
// is live, and the model lookup recovers to "no model".
type Host struct {
	ctx   sdk.Context
	cwd   string
	hasUI bool
}

// New wraps an SDK context. Call it inside the event handler.
func New(ctx sdk.Context) *Host {
	h := &Host{ctx: ctx}
	func() {
		defer func() { _ = recover() }()
		h.cwd = ctx.Cwd()
		h.hasUI = ctx.HasUI()
	}()
	return h
}

// Context is the wrapped SDK context.
func (h *Host) Context() sdk.Context { return h.ctx }

// GetBranch reads the current branch's entries.
func (h *Host) GetBranch() ([]session.Entry, error) {
	entries, err := h.ctx.SessionManager().GetBranch(nil)
	if err != nil {
		return nil, err
	}
	return session.FromMaps(entries), nil
}

// AppendEntry persists a custom entry.
func (h *Host) AppendEntry(customType string, data any) error {
	return h.ctx.AppendEntry(customType, data)
}

// SessionID is the session id, "" when unknown.
func (h *Host) SessionID() string {
	id, err := h.ctx.GetSessionID()
	if err != nil {
		return ""
	}
	return id
}

// SessionFile is the session file path, "" for an in-memory session.
func (h *Host) SessionFile() string {
	file, err := h.ctx.GetSessionFile()
	if err != nil || file == nil {
		return ""
	}
	return *file
}

// HasUI reports whether the host has a UI.
func (h *Host) HasUI() bool { return h.hasUI }

// Notify shows a notification.
func (h *Host) Notify(message, level string) { h.ctx.Notify(message, level) }

// IsIdle reports whether the agent is idle.
func (h *Host) IsIdle() (bool, error) { return h.ctx.IsIdle() }

// Compact starts a compaction and reports its outcome to the callbacks.
func (h *Host) Compact(callbacks host.CompactCallbacks) {
	h.ctx.CompactWithOptions(sdk.CompactOptions{
		OnComplete: func(map[string]any) {
			if callbacks.OnComplete != nil {
				callbacks.OnComplete()
			}
		},
		OnError: func(err error) {
			if callbacks.OnError != nil {
				callbacks.OnError(err)
			}
		},
	})
}

// GetContextUsage reads the context-window usage.
func (h *Host) GetContextUsage() (*host.ContextUsage, error) {
	usage, err := h.ctx.GetContextUsage()
	if err != nil || usage == nil {
		return nil, err
	}
	return &host.ContextUsage{Tokens: usage.Tokens, ContextWindow: usage.ContextWindow}, nil
}

// Cwd is the working directory.
func (h *Host) Cwd() string { return h.cwd }

// SessionModel is the session's active model object, nil when none is set or
// the session has been replaced.
func (h *Host) SessionModel() (model host.Model) {
	defer func() {
		if recover() != nil {
			model = nil
		}
	}()
	provider, id := h.ctx.ModelProvider(), h.ctx.Model()
	if provider != "" && id != "" {
		if model := h.ctx.ModelRegistry().Find(provider, id); model != nil {
			return model
		}
	}
	info, err := h.ctx.GetModelInfo()
	if err != nil || info == nil {
		return nil
	}
	model = host.Model{"provider": info.Provider, "id": info.ID, "name": info.Name, "reasoning": info.Reasoning}
	if info.ContextWindow > 0 {
		model["contextWindow"] = float64(info.ContextWindow)
	}
	if info.MaxOutputTokens > 0 {
		model["maxTokens"] = float64(info.MaxOutputTokens)
	}
	return model
}

// Models is the model registry.
func (h *Host) Models() host.Models { return models{registry: h.ctx.ModelRegistry()} }

type models struct {
	registry sdk.ModelRegistry
}

func (m models) Find(provider, id string) host.Model { return m.registry.Find(provider, id) }

func (m models) GetApiKeyAndHeaders(model host.Model) (host.Auth, error) {
	auth, err := m.registry.GetApiKeyAndHeaders(model)
	if err != nil {
		return host.Auth{}, err
	}
	return host.AuthFromMap(auth), nil
}

func (m models) IsUsingOAuth(model host.Model) bool {
	using, err := m.registry.IsUsingOAuth(model)
	return err == nil && using
}

func (m models) HasConfiguredAuth(model host.Model) bool {
	configured, err := m.registry.HasConfiguredAuth(model)
	return err == nil && configured
}

// Refresh cannot be cancelled: the SDK call takes no context, so a refresh
// that outlives the caller's deadline keeps running in the host until it
// answers. The caller bounds how long it waits, not the host call.
func (m models) Refresh(_ context.Context, options host.RefreshOptions) error {
	_, err := m.registry.Refresh(sdk.ModelsRefreshOptions{AllowNetwork: options.AllowNetwork, Providers: options.Providers})
	return err
}

func (m models) Stream(model host.Model, request map[string]any) host.ModelStream {
	return m.registry.StreamSimple(model, request, nil)
}

// ConfigHome is PiG's config root (PIG_HOME, else XDG_CONFIG_HOME/pig, else
// ~/.pig), "" when it cannot be found.
func (h *Host) ConfigHome() string {
	dir, err := h.ctx.ConfigHome()
	if err != nil {
		return ""
	}
	return dir
}
