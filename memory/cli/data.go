package cli

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/transcript"
)

// harnessData is one harness of assets/harnesses.json, the whole registry as data
// (docs/universal.md, "Harness registry", documents the vocabulary and
// contracts/schemas/registry.schema.json is its schema): where its MCP client reads servers,
// its hooks, and the install steps data cannot put in a config file. The Go code only
// interprets it; what genuinely needs code (a SQLite lookup, a directory search, Amp's message
// lists) is a named resolver the entry references by name.
type harnessData struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	// Class is how the memory tool's MCP server is registered: file (a JSON, YAML or TOML
	// config om edits), own (a file of om's own), manual (steps om prints), none (by an install
	// step, or not at all).
	Class  string            `json:"class"`
	Path   map[string]string `json:"path"`
	Format string            `json:"format"` // json | yaml | toml
	Key    []string          `json:"key"`
	List   bool              `json:"list"`
	// ID is the field a list entry is identified by, when not "name".
	ID string `json:"id"`
	// Entry is the server entry, with the placeholders of expandPlaceholders in any string.
	Entry map[string]any `json:"entry"`
	// Skeleton are the top-level keys a new YAML file needs (Continue's name, version, schema).
	Skeleton map[string]any `json:"skeleton"`
	Manual   string         `json:"manual"`
	// Content is the whole file of an "own" harness (one that loads every file of a pattern
	// from a directory), with the placeholders.
	Content string `json:"content"`
	// Hooks are the harness's lifecycle hooks (HookSpec).
	Hooks *hookData `json:"hooks"`
	// Install are the steps run after the MCP server and the hooks are written, in order (in
	// reverse order on uninstall).
	Install []installStep `json:"install"`
	Note    string        `json:"note"`
	Source  string        `json:"source"`
}

// hookData is a HookSpec as data: the same fields under their JSON names, with the transcript,
// session and record functions as the declarative forms below.
type hookData struct {
	Path                map[string]string `json:"path"`
	Events              map[string]string `json:"events"`
	Transcript          string            `json:"transcript"`
	Matcher             *string           `json:"matcher"`
	TimeoutMS           bool              `json:"timeoutMS"`
	TopLevel            bool              `json:"topLevel"`
	Flat                bool              `json:"flat"`
	PlainContext        bool              `json:"plainContext"`
	TOML                bool              `json:"toml"`
	TimeoutKey          string            `json:"timeoutKey"`
	NoTimeout           bool              `json:"noTimeout"`
	Enable              []string          `json:"enable"`
	NoStdin             bool              `json:"noStdin"`
	Key                 []string          `json:"key"`
	RecordUnlessFound   bool              `json:"recordUnlessFound"`
	SummaryAtPreCompact bool              `json:"summaryAtPreCompact"`
	NoType              bool              `json:"noType"`
	Version             int               `json:"version"`
	ContextField        string            `json:"contextField"`
	TextContext         bool              `json:"textContext"`
	NoContext           bool              `json:"noContext"`
	SummaryOnStart      bool              `json:"summaryOnStart"`
	SessionFromCurrent  bool              `json:"sessionFromCurrent"`
	SummaryOnPrompt     bool              `json:"summaryOnPrompt"`
	// Env names environment variables the harness passes payload fields in (field -> variable).
	Env map[string]string `json:"env"`
	// TranscriptFrom finds the transcript from the payload's cwd and session: the first
	// candidate that yields a path.
	TranscriptFrom []pathCandidate `json:"transcriptFrom"`
	// SessionFrom finds the session id in the payload: the first candidate that yields one.
	SessionFrom []sessionCandidate `json:"sessionFrom"`
	// Record is how om records the turn a payload carries.
	Record *recordData `json:"record"`
}

// pathCandidate is one way to the transcript: a path template (transcriptTemplate), only on
// OS when set, or a named resolver (transcriptResolvers).
type pathCandidate struct {
	OS       string `json:"os"`
	Path     string `json:"path"`
	Resolver string `json:"resolver"`
}

// sessionCandidate is one way to the session id: the payload field Field ("a.b" a nested one)
// when it is a non-empty string not in Not, with Prefix before it; or a named resolver
// (sessionResolvers).
type sessionCandidate struct {
	Field    string   `json:"field"`
	Not      []string `json:"not"`
	Prefix   string   `json:"prefix"`
	Resolver string   `json:"resolver"`
}

// recordData records a turn from payload fields (field -> role), or with a named mapper
// (recordMappers).
type recordData struct {
	Fields map[string]string `json:"fields"`
	Mapper string            `json:"mapper"`
}

// installStep is one named install kind with its parameters (installKinds); every string takes
// the placeholders.
type installStep struct {
	Kind string `json:"kind"`
	// Command and Remove are run-command's argument lists, run on install and on uninstall.
	Command []string `json:"command"`
	Remove  []string `json:"remove"`
	// Dir is a path template: plugin-asset's directory, or print's $DIR.
	Dir string `json:"dir"`
	// Asset is plugin-asset's embedded file, written into Dir under its own name.
	Asset string `json:"asset"`
	// Path is the file of text-file, json-file, managed-line and codex-config.
	Path string `json:"path"`
	// Content is text-file's content; Executable writes it with mode 0755.
	Content    string `json:"content"`
	Executable bool   `json:"executable"`
	// Value is json-file's document, written as om writes any JSON config.
	Value any `json:"value"`
	// Line is managed-line's line, marked as om's.
	Line string `json:"line"`
	// Text and RemoveText are print's messages on install and uninstall.
	Text       string `json:"text"`
	RemoveText string `json:"removeText"`
}

// The named resolvers an entry can reference, for what a template cannot say.
var (
	transcriptResolvers = map[string]func(cwd, sessionID string) string{
		// Kiro's session directory, found by a glob over its workspace buckets.
		"kiro-session": transcript.KiroSessionPath,
		// Crush's database: the nearest .crush directory up to the repository root.
		"crush-db": transcript.CrushDBPath,
	}
	sessionResolvers = map[string]func(raw map[string]any) string{
		// Amazon Q's conversation for the payload's cwd, read from its SQLite database.
		"amazon-q-conversation": func(raw map[string]any) string {
			cwd, _ := raw["cwd"].(string)
			return transcript.AmazonQConversation(cwd)
		},
	}
	recordMappers = map[string]func(raw map[string]any) []map[string]any{
		// Amp's agent.end message lists.
		"amp-messages": ampRecord,
	}
)

// loadData registers every harness of the data file; a malformed entry is an error (the tests
// load the file, so a bad entry never ships).
func loadData(data []byte) error {
	var list []harnessData
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&list); err != nil {
		return fmt.Errorf("harnesses.json: %w", err)
	}
	for _, d := range list {
		h, err := d.harness()
		if err != nil {
			return fmt.Errorf("harnesses.json: %s: %w", d.Name, err)
		}
		if _, dup := registry[h.Name]; dup {
			return fmt.Errorf("harnesses.json: %s is registered twice", h.Name)
		}
		register(h)
	}
	return nil
}

func checkOS(paths map[string]string) error {
	for goos := range paths {
		if goos != "" && goos != "darwin" && goos != "linux" && goos != "windows" {
			return fmt.Errorf("unknown OS %q in path", goos)
		}
	}
	return nil
}

func (d harnessData) harness() (Harness, error) {
	if d.Name == "" || d.Title == "" {
		return Harness{}, fmt.Errorf("needs a name and a title")
	}
	h := Harness{Name: d.Name, Title: d.Title, Note: d.Note, Source: d.Source}
	if err := d.mcp(&h); err != nil {
		return h, err
	}
	if d.Hooks != nil {
		spec, err := d.Hooks.spec()
		if err != nil {
			return h, fmt.Errorf("hooks: %w", err)
		}
		h.Hooks = spec
	}
	if len(d.Install) > 0 {
		custom, err := installSteps(d.Name, d.Install)
		if err != nil {
			return h, err
		}
		h.Custom = custom
	}
	return h, nil
}

// mcp sets the harness's MCP registration from its class.
func (d harnessData) mcp(h *Harness) error {
	switch d.Class {
	case "none":
		return nil
	case "manual":
		if d.Manual == "" {
			return fmt.Errorf("a manual harness needs its steps")
		}
		h.Manual = d.Manual
		return nil
	case "own":
		if len(d.Path) == 0 || d.Content == "" {
			return fmt.Errorf("an own-file harness needs a path and its content")
		}
		content := d.Content
		h.Own = &OwnSpec{Path: d.Path, Content: func(exe, harness string) string {
			return expandPlaceholders(content, exe, harness, "")
		}}
		return checkOS(d.Path)
	case "file":
	default:
		return fmt.Errorf("unknown class %q", d.Class)
	}
	if len(d.Path) == 0 || len(d.Key) == 0 || d.Entry == nil {
		return fmt.Errorf("a file harness needs a path, a key and an entry")
	}
	if err := checkOS(d.Path); err != nil {
		return err
	}
	entry := func(exe, harness string) map[string]any {
		return expandEntry(d.Entry, exe, harness).(map[string]any)
	}
	if d.Skeleton != nil && d.Format != "yaml" {
		return fmt.Errorf("a skeleton is for a YAML file")
	}
	switch d.Format {
	case "json":
		h.MCP = &MCPSpec{Path: d.Path, Key: d.Key, Entry: entry, List: d.List, ID: d.ID}
	case "yaml":
		if len(d.Key) != 1 {
			return fmt.Errorf("a YAML key is one top-level key")
		}
		h.YAML = &YAMLSpec{Path: d.Path, Key: d.Key[0], List: d.List, Entry: entry, Skeleton: d.Skeleton}
	case "toml":
		h.TOML = &TOMLSpec{Path: d.Path, Table: strings.Join(d.Key, "."), Entry: entry, List: d.List}
	default:
		return fmt.Errorf("unknown format %q", d.Format)
	}
	return nil
}

// spec is the HookSpec the data declares.
func (d *hookData) spec() (*HookSpec, error) {
	if len(d.Events) == 0 || d.Transcript == "" {
		return nil, fmt.Errorf("hooks need events and a transcript format")
	}
	for ev := range d.Events {
		if _, ok := hookTimeouts[ev]; !ok {
			return nil, fmt.Errorf("unknown om event %q", ev)
		}
	}
	if _, file := transcript.Formats[d.Transcript]; !file {
		if _, db := transcript.DBFormats[d.Transcript]; !db {
			return nil, fmt.Errorf("unknown transcript format %q", d.Transcript)
		}
	}
	if err := checkOS(d.Path); err != nil {
		return nil, err
	}
	s := &HookSpec{
		Path: d.Path, Events: d.Events, Transcript: d.Transcript, Matcher: d.Matcher, TimeoutMS: d.TimeoutMS,
		TopLevel: d.TopLevel, Flat: d.Flat, PlainContext: d.PlainContext, TOML: d.TOML, TimeoutKey: d.TimeoutKey,
		NoTimeout: d.NoTimeout, Enable: d.Enable, NoStdin: d.NoStdin, Key: d.Key, RecordUnlessFound: d.RecordUnlessFound,
		SummaryAtPreCompact: d.SummaryAtPreCompact, NoType: d.NoType, Version: d.Version, ContextField: d.ContextField,
		TextContext: d.TextContext, NoContext: d.NoContext, SummaryOnStart: d.SummaryOnStart,
		SessionFromCurrent: d.SessionFromCurrent, EnvFields: d.Env, SummaryOnPrompt: d.SummaryOnPrompt,
	}
	if len(d.TranscriptFrom) > 0 {
		f, err := transcriptFrom(d.TranscriptFrom)
		if err != nil {
			return nil, err
		}
		s.TranscriptFrom = f
	}
	if len(d.SessionFrom) > 0 {
		f, err := sessionFrom(d.SessionFrom)
		if err != nil {
			return nil, err
		}
		s.SessionFrom = f
	}
	if r := d.Record; r != nil {
		switch {
		case r.Mapper != "" && len(r.Fields) == 0:
			m, ok := recordMappers[r.Mapper]
			if !ok {
				return nil, fmt.Errorf("unknown record mapper %q", r.Mapper)
			}
			s.Record = m
		case r.Mapper == "" && len(r.Fields) > 0:
			for _, role := range r.Fields {
				if role != "user" && role != "assistant" {
					return nil, fmt.Errorf("unknown record role %q", role)
				}
			}
			s.Record = recordFields(r.Fields)
		default:
			return nil, fmt.Errorf("a record is fields or a mapper")
		}
	}
	if s.RecordUnlessFound && (s.Record == nil || s.TranscriptFrom == nil) {
		return nil, fmt.Errorf("recordUnlessFound needs a record and transcriptFrom")
	}
	return s, nil
}

// transcriptFrom is the first of the candidates that yields a path.
func transcriptFrom(cands []pathCandidate) (func(cwd, sessionID string) string, error) {
	for _, c := range cands {
		if (c.Path == "") == (c.Resolver == "") {
			return nil, fmt.Errorf("a transcriptFrom candidate is a path or a resolver")
		}
		if c.Resolver != "" {
			if _, ok := transcriptResolvers[c.Resolver]; !ok {
				return nil, fmt.Errorf("unknown transcript resolver %q", c.Resolver)
			}
		}
		if c.Path != "" {
			if _, err := transcriptTemplate(c.Path, "c", "s"); err != nil && err != errNoValue {
				return nil, err
			}
		}
		if c.OS != "" && c.OS != "darwin" && c.OS != "linux" && c.OS != "windows" {
			return nil, fmt.Errorf("unknown OS %q", c.OS)
		}
	}
	return func(cwd, sessionID string) string {
		for _, c := range cands {
			if c.OS != "" && c.OS != runtime.GOOS {
				continue
			}
			p := ""
			if c.Resolver != "" {
				p = transcriptResolvers[c.Resolver](cwd, sessionID)
			} else {
				p, _ = transcriptTemplate(c.Path, cwd, sessionID)
			}
			if p != "" {
				return p
			}
		}
		return ""
	}, nil
}

// errNoValue is a template's variable with no value: the candidate yields nothing.
var errNoValue = fmt.Errorf("no value")

// transcriptTemplate expands a transcript path template: ${cwd} and ${session} are the
// payload's, ${md5(cwd)} the hex MD5 of the cwd, ${slug(cwd)} the cwd with every byte that is
// not an ASCII letter or digit as -, ${VAR} and ${VAR:-default} the environment's, and a
// leading ~ the home directory. A payload value or an environment variable without a default
// that is empty leaves the template without a path (errNoValue); the result is cleaned. A
// payload value is inserted as it is, never expanded again.
func transcriptTemplate(t, cwd, sessionID string) (string, error) {
	var b strings.Builder
	tilde := strings.HasPrefix(t, "~")
	for i := 0; i < len(t); {
		start := strings.Index(t[i:], "${")
		if start < 0 {
			b.WriteString(t[i:])
			break
		}
		b.WriteString(t[i : i+start])
		end := strings.IndexByte(t[i+start:], '}')
		if end < 0 {
			return "", fmt.Errorf("unclosed ${ in %q", t)
		}
		inner := t[i+start+2 : i+start+end]
		atStart := i+start == 0
		i += start + end + 1
		var v string
		switch inner {
		case "cwd":
			v = cwd
		case "session":
			v = sessionID
		case "md5(cwd)":
			if cwd != "" {
				sum := md5.Sum([]byte(cwd))
				v = hex.EncodeToString(sum[:])
			}
		case "slug(cwd)":
			slug := []byte(cwd)
			for j, c := range slug {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
					slug[j] = '-'
				}
			}
			v = string(slug)
		default:
			name, def, hasDef := strings.Cut(inner, ":-")
			if name == "" || strings.ContainsAny(name, "(){}$ ") {
				return "", fmt.Errorf("bad variable %q in %q", inner, t)
			}
			v = os.Getenv(name)
			if v == "" && hasDef {
				v = def
				tilde = tilde || atStart && strings.HasPrefix(def, "~")
			}
		}
		if v == "" {
			return "", errNoValue
		}
		b.WriteString(v)
	}
	out := b.String()
	if tilde && (out == "~" || strings.HasPrefix(out, "~/")) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errNoValue
		}
		out = home + strings.TrimPrefix(out, "~")
	}
	return filepath.Clean(filepath.FromSlash(out)), nil
}

// sessionFrom is the first of the candidates that yields a session id.
func sessionFrom(cands []sessionCandidate) (func(raw map[string]any) string, error) {
	for _, c := range cands {
		if (c.Field == "") == (c.Resolver == "") {
			return nil, fmt.Errorf("a sessionFrom candidate is a field or a resolver")
		}
		if _, ok := sessionResolvers[c.Resolver]; c.Resolver != "" && !ok {
			return nil, fmt.Errorf("unknown session resolver %q", c.Resolver)
		}
	}
	return func(raw map[string]any) string {
		for _, c := range cands {
			if c.Resolver != "" {
				if id := sessionResolvers[c.Resolver](raw); id != "" {
					return id
				}
				continue
			}
			v := rawString(c.Field)(raw)
			skip := v == ""
			for _, not := range c.Not {
				skip = skip || v == not
			}
			if !skip {
				return c.Prefix + v
			}
		}
		return ""
	}, nil
}

// installKinds are the install steps an entry can name.
var installKinds = map[string]func(s installStep, harness string) (func(w writer, exe string, remove bool) error, error){
	// run-command runs a command (Claude Code's own MCP command, pig install).
	"run-command": func(s installStep, harness string) (func(w writer, exe string, remove bool) error, error) {
		if len(s.Command) == 0 {
			return nil, fmt.Errorf("run-command needs a command")
		}
		return func(w writer, exe string, remove bool) error {
			argv := s.Command
			if remove {
				argv = s.Remove
			}
			if len(argv) > 0 {
				args := make([]string, len(argv))
				for i, a := range argv {
					args[i] = expandPlaceholders(a, exe, harness, "")
				}
				w.run(args[0], args[1:]...)
			}
			return nil
		}, nil
	},
	// plugin-asset writes an embedded plugin or extension file into a directory.
	"plugin-asset": func(s installStep, harness string) (func(w writer, exe string, remove bool) error, error) {
		if s.Dir == "" || s.Asset == "" {
			return nil, fmt.Errorf("plugin-asset needs a dir and an asset")
		}
		if _, err := assets.ReadFile(s.Asset); err != nil {
			return nil, fmt.Errorf("plugin-asset: %w", err)
		}
		return assetInstaller(harness, s.Dir, s.Asset), nil
	},
	// text-file writes a file of om's own.
	"text-file": func(s installStep, harness string) (func(w writer, exe string, remove bool) error, error) {
		if s.Path == "" || s.Content == "" {
			return nil, fmt.Errorf("text-file needs a path and content")
		}
		mode := os.FileMode(0o644)
		if s.Executable {
			mode = 0o755
		}
		return func(w writer, exe string, remove bool) error {
			path := pathTemplate(s.Path)
			if remove {
				return w.remove(path)
			}
			return w.writeMode(path, []byte(expandPlaceholders(s.Content, exe, harness, "")), mode)
		}, nil
	},
	// json-file writes a JSON file of om's own.
	"json-file": func(s installStep, harness string) (func(w writer, exe string, remove bool) error, error) {
		if s.Path == "" || s.Value == nil {
			return nil, fmt.Errorf("json-file needs a path and a value")
		}
		return func(w writer, exe string, remove bool) error {
			path := pathTemplate(s.Path)
			if remove {
				return w.remove(path)
			}
			data, err := encodeJSON(expandEntry(s.Value, exe, harness))
			if err != nil {
				return err
			}
			return w.write(path, data)
		}, nil
	},
	// managed-line appends one line marked as om's to a file of commands (Crush's crushrc).
	"managed-line": func(s installStep, harness string) (func(w writer, exe string, remove bool) error, error) {
		if s.Path == "" || s.Line == "" {
			return nil, fmt.Errorf("managed-line needs a path and a line")
		}
		return func(w writer, exe string, remove bool) error {
			return installManagedLine(w, pathTemplate(s.Path), expandPlaceholders(s.Line, exe, harness, ""), remove)
		}, nil
	},
	// print prints what the user adds by hand (Amazon Q's agent file).
	"print": func(s installStep, harness string) (func(w writer, exe string, remove bool) error, error) {
		if s.Text == "" || s.RemoveText == "" {
			return nil, fmt.Errorf("print needs a text and a removeText")
		}
		return func(w writer, exe string, remove bool) error {
			text := s.Text
			if remove {
				text = s.RemoveText
			}
			_, err := fmt.Fprint(w.out, expandPlaceholders(text, exe, harness, pathTemplate(s.Dir)))
			return err
		}, nil
	},
	// codex-config adds the MCP server and the notify program to Codex's config.toml.
	"codex-config": func(s installStep, harness string) (func(w writer, exe string, remove bool) error, error) {
		if s.Path == "" {
			return nil, fmt.Errorf("codex-config needs a path")
		}
		return func(w writer, exe string, remove bool) error {
			return installCodex(w, pathTemplate(s.Path), exe, harness, remove)
		}, nil
	},
}

// installSteps is the harness's install steps as one Custom step: in order on install, in
// reverse order on uninstall.
func installSteps(harness string, steps []installStep) (func(w writer, exe string, remove bool) error, error) {
	var fns []func(w writer, exe string, remove bool) error
	for _, s := range steps {
		kind, ok := installKinds[s.Kind]
		if !ok {
			return nil, fmt.Errorf("unknown install kind %q", s.Kind)
		}
		fn, err := kind(s, harness)
		if err != nil {
			return nil, err
		}
		fns = append(fns, fn)
	}
	return func(w writer, exe string, remove bool) error {
		for i := range fns {
			fn := fns[i]
			if remove {
				fn = fns[len(fns)-1-i]
			}
			if err := fn(w, exe, remove); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

// expandPlaceholders replaces the registry's placeholders in s: $OM is om's path, $OM_SH that
// path quoted for a POSIX shell, $OM_SH_ESCAPED $OM_SH escaped for a double-quoted string,
// $HARNESS the harness name and $DIR a print step's directory.
func expandPlaceholders(s, exe, harness, dir string) string {
	sh := shellQuote(exe)
	escaped := strconv.Quote(sh)
	return strings.NewReplacer(
		"$OM_SH_ESCAPED", escaped[1:len(escaped)-1],
		"$OM_SH", sh,
		"$OM", exe,
		"$HARNESS", harness,
		"$DIR", dir,
	).Replace(s)
}

// expandEntry replaces the placeholders in every string of v.
func expandEntry(v any, exe, harness string) any {
	switch x := v.(type) {
	case string:
		return expandPlaceholders(x, exe, harness, "")
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = expandEntry(e, exe, harness)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out[k] = expandEntry(x[k], exe, harness)
		}
		return out
	}
	return v
}
