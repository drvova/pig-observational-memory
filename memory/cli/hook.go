package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/memory/engine"
	"github.com/drvova/pig-observational-memory/memory/filelock"
	"github.com/drvova/pig-observational-memory/memory/hooks"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/neutral"
)

// hook runs one harness event (om hook <harness> <event>), its payload on in (or, for Codex's
// notify, in args), writing to out what the harness reads back.
func (a *app) hook(harnessName, event string, args []string, in io.Reader, out io.Writer) error {
	switch harnessName {
	case "codex":
		if event == "notify" {
			return a.codexHook(event, args)
		}
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
	if hs.Hooks.NoStdin {
		in = strings.NewReader("")
	}
	p, err := decodeHookPayload(in, hs.Hooks.EnvFields)
	if err != nil {
		return fmt.Errorf("om hook %s: payload: %w", hs.Name, err)
	}
	if hs.Hooks.SessionFrom != nil {
		if id := hs.Hooks.SessionFrom(p.Raw); id != "" {
			p.SessionID = id
		}
	}
	if p.Cwd == "" {
		p.Cwd, _ = os.Getwd()
	}
	if p.SessionID == "" && hs.Hooks.SessionFromCurrent {
		if cur, ok := current(a.root, hs.Name, p.Cwd); ok {
			p.SessionID = cur.ID
		}
	}
	if hs.Hooks.TranscriptFrom != nil {
		p.TranscriptPath = hs.Hooks.TranscriptFrom(p.Cwd, p.SessionID)
	}
	format := hs.Hooks.Transcript
	if hs.Hooks.Record != nil && !(hs.Hooks.RecordUnlessFound && p.TranscriptPath != "") {
		if p.SessionID == "" {
			return fmt.Errorf("om hook %s: the payload names no session", hs.Name)
		}
		if p.TranscriptPath, err = recordTurn(a.root, hs, p); err != nil {
			return err
		}
		format = "generic"
	}
	s := Session{Harness: hs.Name, ID: p.SessionID, Transcript: p.TranscriptPath, Format: format, Cwd: p.Cwd}
	h, err := s.Host(a.root, a.log)
	if err != nil {
		return err
	}
	release, err := lockEngine(h)
	if err != nil {
		return err
	}
	defer release()
	if err := remember(a.root, s); err != nil {
		return err
	}
	switch ev {
	case evSessionStart:
		engine.SessionStart(a.rt, a.manager, &a.pin, h)
		summary := ""
		if p.Source == "compact" || p.Source == "resume" || hs.Hooks.SummaryOnStart {
			summary, _ = h.LatestSummary()
		}
		return writeContext(out, hs, name, a.startContext(h, summary))
	case evPrompt:
		if _, turnEnd := hs.Hooks.Events[evTurnEnd]; !turnEnd {
			hooks.OnTurnEnd(a.rt, h)
			engine.SettleCurated(h)
		}
		if !hs.Hooks.SummaryOnPrompt {
			return nil
		}
		summary, err := summaryToHand(h)
		if err != nil || summary == "" {
			return err
		}
		return writeContext(out, hs, name, a.startContext(h, summary))
	case evTurnEnd:
		hooks.OnTurnEnd(a.rt, h)
		engine.SettleCurated(h)
	case evPreCompact:
		c, err := h.Compaction(context.Background(), a.rt, p.Trigger)
		if err != nil || !hs.Hooks.SummaryAtPreCompact || c == nil {
			return err
		}
		_, err = io.WriteString(out, c.Summary+"\n")
		return err
	}
	return nil
}

// writeContext writes context for the model in the shape the harness reads.
func writeContext(out io.Writer, hs Harness, name, context string) error {
	switch {
	case hs.Hooks.NoContext:
		return nil
	case hs.Hooks.TextContext:
		_, err := io.WriteString(out, context+"\n")
		return err
	case hs.Hooks.PlainContext:
		field := hs.Hooks.ContextField
		if field == "" {
			field = "additionalContext"
		}
		return json.NewEncoder(out).Encode(map[string]any{field: context})
	}
	return json.NewEncoder(out).Encode(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": name, "additionalContext": context}})
}

// summaryHanded marks the compaction whose summary a prompt hook has handed the model.
const summaryHanded = "om.summary.handed"

// summaryToHand is the latest compaction's summary when no prompt hook has handed it yet, and
// records that it now has: for a harness whose session-start hook never says it compacted
// (HookSpec.SummaryOnPrompt), the first prompt after a compaction carries the summary, once.
func summaryToHand(h *neutral.Host) (string, error) {
	branch, err := h.GetBranch()
	if err != nil {
		return "", err
	}
	i := ledger.FindLastCompactionIndex(branch)
	if i < 0 {
		return "", nil
	}
	compaction := branch[i].ID
	for _, e := range branch[i+1:] {
		if e.CustomType != summaryHanded {
			continue
		}
		if data, ok := e.Data.(map[string]any); ok && data["compaction"] == compaction {
			return "", nil
		}
	}
	summary, _ := branch[i].Summary.(string)
	if summary == "" {
		return "", nil
	}
	if err := h.AppendEntry(summaryHanded, map[string]any{"compaction": compaction}); err != nil {
		return "", err
	}
	return summary, nil
}

// hookPayload is what om reads from a hook's stdin, under the field names the harness families
// use: Claude Code's snake_case, the camelCase of Copilot's events, Cursor's conversation_id and
// workspace_roots, OpenHands' working_dir.
type hookPayload struct {
	SessionID, TranscriptPath, Cwd, Source, Trigger string
	// Raw is the whole payload, for a harness whose turn om records itself (HookSpec.Record).
	Raw map[string]any
}

// decodeHookPayload reads a hook's stdin JSON (empty stdin is an empty payload); env names
// the environment variables a harness passes fields in instead (HookSpec.EnvFields).
func decodeHookPayload(in io.Reader, env map[string]string) (hookPayload, error) {
	raw := map[string]any{}
	data, err := io.ReadAll(in)
	if err != nil {
		return hookPayload{}, err
	}
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &raw); err != nil {
			return hookPayload{}, err
		}
	}
	for field, name := range env {
		if v := os.Getenv(name); v != "" {
			if s, _ := raw[field].(string); s == "" {
				raw[field] = v
			}
		}
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
		Cwd:            first("cwd", "working_dir", "working_directory"),
		Source:         first("source"),
		Trigger:        first("trigger"),
		Raw:            raw,
	}
	for _, k := range []string{"workspace_roots", "workspaceRoots"} {
		if roots, ok := raw[k].([]any); ok && len(roots) > 0 && p.Cwd == "" {
			p.Cwd, _ = roots[0].(string)
		}
	}
	return p, nil
}

// recordTurn appends the turn a hook payload carries (HookSpec.Record) to om's own transcript
// of the session, ~/.om/transcripts/<harness>/<session>.jsonl in the generic format, each line
// given the next id; it returns that transcript's path.
func recordTurn(root string, hs Harness, p hookPayload) (string, error) {
	path := filepath.Join(root, "transcripts", hs.Name, neutral.SafeName(p.SessionID)+".jsonl")
	lines := hs.Hooks.Record(p.Raw)
	if len(lines) == 0 {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	return path, filelock.WithLock(path+".lock", func() error {
		n := 0
		if data, err := os.ReadFile(path); err == nil {
			n = strings.Count(string(data), "\n")
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for _, l := range lines {
			n++
			l["id"] = fmt.Sprintf("t%06d", n)
			if _, ok := l["timestamp"]; !ok {
				l["timestamp"] = now
			}
			b, err := json.Marshal(l)
			if err != nil {
				return err
			}
			if _, err := f.Write(append(b, '\n')); err != nil {
				return err
			}
		}
		return nil
	})
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
	rollout := ""
	if cur, ok := current(a.root, "codex", p.Cwd); ok && cur.ID == p.Thread {
		if _, err := os.Stat(cur.Transcript); err == nil {
			rollout = cur.Transcript
		}
	}
	var err error
	if rollout == "" {
		rollout, err = codexRollout(p.Thread)
	}
	if err != nil {
		return err
	}
	s := Session{Harness: "codex", ID: p.Thread, Transcript: rollout, Format: "codex", Cwd: p.Cwd}
	h, err := s.Host(a.root, a.log)
	if err != nil {
		return err
	}
	release, err := lockEngine(h)
	if err != nil {
		return err
	}
	defer release()
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
	// A turn's rollout is almost always in a recent day's folder (sessions/YYYY/MM/DD): look
	// there before walking the whole tree, which grows with every session.
	for back := 0; back < 3; back++ {
		day := time.Now().AddDate(0, 0, -back).Format("2006/01/02")
		if matches, _ := filepath.Glob(filepath.Join(home, "sessions", filepath.FromSlash(day), "*"+thread+".jsonl")); len(matches) > 0 {
			return matches[0], nil
		}
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
	release, err := lockEngine(h)
	if err != nil {
		return err
	}
	defer release()
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
	release, err := lockEngine(h)
	if err != nil {
		return err
	}
	defer release()
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

// lockEngine holds the session's engine lock (beside its store) for one hook's engine work: each
// hook is its own process, and two at once on one session (Codex's notify is not waited on, a
// plugin's idle event can repeat) would each extract the same turns.
func lockEngine(h *neutral.Host) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(h.Store.Path), 0o700); err != nil {
		return nil, err
	}
	return filelock.Lock(h.Store.Path + ".engine.lock")
}
