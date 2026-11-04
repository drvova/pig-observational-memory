package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/engine"
	"github.com/drvova/pig-observational-memory/memory/hooks"
)

// hook runs one harness event (om hook <harness> <event>), its payload on in (or, for Codex's
// notify, in args), writing to out what the harness reads back.
func (a *app) hook(harnessName, event string, args []string, in io.Reader, out io.Writer) error {
	switch harnessName {
	case "codex":
		return a.codexHook(event, args)
	case "pi":
		return a.piHook(event, in, out)
	case "generic":
		return a.genericHook(event, in, out)
	}
	if h, ok := registry[harnessName]; ok && h.Hooks != nil {
		return a.familyHook(h, event, in, out)
	}
	return fmt.Errorf("om hook: harness %q has no hooks (om harnesses lists them; any harness can use om hook generic)", harnessName)
}

// familyHook answers a hook of a harness that speaks Claude Code's hook schema
// (docs/universal.md, "Hook families"). Every event records the session as current for the MCP
// server; session-start hands the model the memory context (after a compaction, the engine's
// summary beside the harness's own); turn-end runs level 0; pre-compact runs the engine's
// compaction of the whole conversation before the harness's.
func (a *app) familyHook(hs Harness, name string, in io.Reader, out io.Writer) error {
	ev := ""
	for omEvent, harnessEvent := range hs.Hooks.Events {
		if harnessEvent == name {
			ev = omEvent
		}
	}
	if ev == "" {
		return fmt.Errorf("om hook %s: unknown event %q", hs.Name, name)
	}
	p, err := decodeHookPayload(in)
	if err != nil {
		return fmt.Errorf("om hook %s: payload: %w", hs.Name, err)
	}
	s := Session{Harness: hs.Name, ID: p.SessionID, Transcript: p.TranscriptPath, Format: hs.Hooks.Transcript, Cwd: p.Cwd}
	h, err := s.Host(a.root, a.log)
	if err != nil {
		return err
	}
	if err := remember(a.root, s); err != nil {
		return err
	}
	switch ev {
	case evSessionStart:
		engine.SessionStart(a.rt, a.manager, &a.pin, h)
		summary := ""
		if p.Source == "compact" || p.Source == "resume" {
			summary, _ = h.LatestSummary()
		}
		return json.NewEncoder(out).Encode(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": name, "additionalContext": a.startContext(h, summary)}})
	case evTurnEnd:
		hooks.OnTurnEnd(a.rt, h)
		engine.SettleCurated(h)
	case evPreCompact:
		_, err := h.Compaction(context.Background(), a.rt, p.Trigger)
		return err
	}
	return nil
}

// hookPayload is what om reads from a hook's stdin, under the field names the harness families
// use: Claude Code's snake_case, the camelCase of Copilot's events, Cursor's conversation_id and
// workspace_roots, OpenHands' working_dir.
type hookPayload struct {
	SessionID, TranscriptPath, Cwd, Source, Trigger string
}

func decodeHookPayload(in io.Reader) (hookPayload, error) {
	var raw map[string]any
	if err := json.NewDecoder(in).Decode(&raw); err != nil {
		return hookPayload{}, err
	}
	first := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := raw[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	p := hookPayload{
		SessionID:      first("session_id", "sessionId", "conversation_id", "conversationId"),
		TranscriptPath: first("transcript_path", "transcriptPath"),
		Cwd:            first("cwd", "working_dir"),
		Source:         first("source"),
		Trigger:        first("trigger"),
	}
	if p.Cwd == "" {
		if roots, ok := raw["workspace_roots"].([]any); ok && len(roots) > 0 {
			p.Cwd, _ = roots[0].(string)
		}
	}
	return p, nil
}

// codexHook answers Codex's notify program (om hook codex notify '<json>'), called after each
// agent turn with the turn as its last argument: level 0 over the rollout of the thread.
func (a *app) codexHook(event string, args []string) error {
	if event != "notify" || len(args) == 0 {
		return fmt.Errorf("om hook codex: want notify '<payload>'")
	}
	var p struct {
		Type   string `json:"type"`
		Thread string `json:"thread-id"`
		Cwd    string `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(args[len(args)-1]), &p); err != nil {
		return fmt.Errorf("om hook codex: payload: %w", err)
	}
	if p.Type != "agent-turn-complete" || p.Thread == "" {
		return nil
	}
	rollout, err := codexRollout(p.Thread)
	if err != nil {
		return err
	}
	s := Session{Harness: "codex", ID: p.Thread, Transcript: rollout, Format: "codex", Cwd: p.Cwd}
	h, err := s.Host(a.root, a.log)
	if err != nil {
		return err
	}
	if err := remember(a.root, s); err != nil {
		return err
	}
	hooks.OnTurnEnd(a.rt, h)
	engine.SettleCurated(h)
	return nil
}

// codexRollout is the rollout file of a Codex thread: the one under $CODEX_HOME/sessions
// (~/.codex) whose name ends with the thread id.
func codexRollout(thread string) (string, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		dir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(dir, ".codex")
	}
	found := ""
	err := filepath.WalkDir(filepath.Join(home, "sessions"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), thread+".jsonl") {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if found == "" {
		return "", fmt.Errorf("om hook codex: no rollout for thread %s under %s: %v", thread, home, err)
	}
	return found, nil
}

// event is the generic protocol's payload (docs/universal.md, "Any other harness").
type event struct {
	Harness        string `json:"harness"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Format         string `json:"format"`
	Cwd            string `json:"cwd"`
	Reason         string `json:"reason"`
	// FirstKeptEntryID and TokensBefore are a harness's own cut (pi): compaction keeps the
	// entries from FirstKeptEntryID on, and the harness stores the result.
	FirstKeptEntryID string `json:"first_kept_entry_id"`
	TokensBefore     int    `json:"tokens_before"`
}

func (e event) session(defaultHarness, defaultFormat string) Session {
	s := Session{Harness: e.Harness, ID: e.SessionID, Transcript: e.TranscriptPath, Format: e.Format, Cwd: e.Cwd}
	if s.Harness == "" {
		s.Harness = defaultHarness
	}
	if s.Format == "" {
		s.Format = defaultFormat
	}
	return s
}

// genericHook is the protocol any harness can speak (om hook generic <event>, the payload on
// stdin): session-start prints the memory context, turn-end runs level 0, compact runs the
// engine's compaction of everything so far and prints its summary for the harness to keep in
// its context.
func (a *app) genericHook(name string, in io.Reader, out io.Writer) error {
	var e event
	if err := json.NewDecoder(in).Decode(&e); err != nil {
		return fmt.Errorf("om hook generic: payload: %w", err)
	}
	s := e.session("generic", "generic")
	h, err := s.Host(a.root, a.log)
	if err != nil {
		return err
	}
	if err := remember(a.root, s); err != nil {
		return err
	}
	switch name {
	case "session-start":
		engine.SessionStart(a.rt, a.manager, &a.pin, h)
		summary, _ := h.LatestSummary()
		_, err := io.WriteString(out, a.startContext(h, summary)+"\n")
		return err
	case "turn-end":
		hooks.OnTurnEnd(a.rt, h)
		engine.SettleCurated(h)
		return nil
	case "compact":
		c, err := h.Compaction(context.Background(), a.rt, e.Reason)
		if err != nil {
			return err
		}
		_, err = io.WriteString(out, c.Summary+"\n")
		return err
	}
	return fmt.Errorf("om hook generic: unknown event %q (session-start, turn-end, compact)", name)
}

// piHook is the Pi extension's protocol (adapters/pi): the generic events over Pi's session
// file, except that before-compact compacts on Pi's own cut and prints the compaction as JSON
// for the extension to return from session_before_compact (Pi stores it).
func (a *app) piHook(name string, in io.Reader, out io.Writer) error {
	var e event
	if err := json.NewDecoder(in).Decode(&e); err != nil {
		return fmt.Errorf("om hook pi: payload: %w", err)
	}
	s := e.session("pi", "pi")
	h, err := s.Host(a.root, a.log)
	if err != nil {
		return err
	}
	if err := remember(a.root, s); err != nil {
		return err
	}
	switch name {
	case "session-start":
		engine.SessionStart(a.rt, a.manager, &a.pin, h)
		_, err := io.WriteString(out, a.startContext(h, "")+"\n")
		return err
	case "turn-end":
		hooks.OnTurnEnd(a.rt, h)
		engine.SettleCurated(h)
		return nil
	case "before-compact":
		var c *hooks.CompactionResult
		if e.FirstKeptEntryID != "" {
			c, err = h.CompactionAt(context.Background(), a.rt, e.Reason, e.FirstKeptEntryID, e.TokensBefore)
		} else {
			c, err = h.Compaction(context.Background(), a.rt, e.Reason)
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"compaction": c.ToMap()})
	}
	return fmt.Errorf("om hook pi: unknown event %q (session-start, turn-end, before-compact)", name)
}
