package omhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
)

// The call context (docs/code-tool.md, "Per-call bindings"): what one memory tool call's om calls
// are answered from. It lives only in the extension; the child knows the call's id and strings,
// nothing else. Every answer is plain data copied out of the snapshot, encoded as JSON, and
// holds no reference to it.

// Layer is the layer access a call carries (harness sets it): the session model's forget and
// rollback, which never touch a rule or another model's layer, and the listing of every model's
// layer. Each write names the model it acted on and tells the user itself.
type Layer interface {
	Forget(kind, name string) (string, error)
	Rollback(gen int) (string, error) // to generation gen
	Listing() ([]om.LayerRow, error)
}

// Units is the kept-definitions store a namespace call carries (the code store and the
// supervisor, wired by the memory tool): kept, forget and the manual's store part.
type Units interface {
	Kept(names []string) (string, error)
	Forget(name string) (string, error)
	ManualPart() string
}

// Call is one memory tool call's context.
type Call struct {
	// Entries are the current branch, a snapshot taken before the call ran.
	Entries []session.Entry
	// SessionEntries are every entry of the session file, for the off-path fallback; nil when
	// the host cannot read them.
	SessionEntries []session.Entry
	// Skip are the tools whose calls are memory plumbing: the memory tool and the retired
	// defined tools. History and the quote checks leave their calls out.
	Skip []string
	// Curated is the curated store as seen from this project and session; nil when curated
	// memory is off, and Saved, recall of a name and the curated writes then fail. Its evidence
	// closures, when nil, are built from Entries and Skip.
	Curated *curated.Call
	// Notify tells the user of a save or an edit; nil tells no one.
	Notify func(message string)
	// Layer is the layer access; nil refuses Layer, LayerForget and LayerRollback.
	Layer Layer
	// Info is om.Info's answer (commands.StatusFigures, injected by the root package); nil
	// refuses it.
	Info func() om.StatusInfo
	// Retired lists the retired defined queries (legacyquery over the curated store); nil lists
	// none.
	Retired func() ([]om.RetiredQuery, error)
	// Units is the kept-definitions store; nil refuses kept and forget and prints no store part.
	Units Units
	// Record is what the call did, for its Details; nil records nothing.
	Record *Record

	// detector is the pass a detector run reads (DetectorCall), nil for a memory tool call.
	detector *DetectorPass
}

// Write is one curated or layer write that landed.
type Write struct {
	Op          string `json:"op"`
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

// The write ops a Write names.
const (
	WriteSave          = "save"
	WriteEdit          = "edit"
	WriteForgetMemory  = "forget"
	WriteFeedback      = "feedback"
	WriteLayerForget   = "layer-forget"
	WriteLayerRollback = "layer-rollback"
)

// Record is the hit recorder (docs/code-tool.md, "Result"): what the om calls of one memory tool
// call read and wrote, kept in the extension and safe to read while the call runs, so a result
// built after a cancel or a crash still lists every write that landed.
type Record struct {
	mu       sync.Mutex
	writes   []Write
	hits     []string
	recalled []string
	searches []string
	reads    int
	found    int
}

// Recorded is a Record's contents.
type Recorded struct {
	// Writes are the writes that landed, in order.
	Writes []Write
	// Hits are the record ids, reflection ids and saved names the reads returned, and the recall
	// ids, each once in first-seen order.
	Hits []string
	// Recalled are the ids passed to Recall.
	Recalled []string
	// Searches are the words of every Search.
	Searches []string
	// Reads counts the reads answered; Empty is true when at least one ran and every one
	// returned nothing.
	Reads int
	Empty bool
}

// Snapshot copies what the record holds now.
func (r *Record) Snapshot() Recorded {
	if r == nil {
		return Recorded{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return Recorded{Writes: slices.Clone(r.writes), Hits: slices.Clone(r.hits), Recalled: slices.Clone(r.recalled), Searches: slices.Clone(r.searches), Reads: r.reads, Empty: r.reads > 0 && r.found == 0}
}

func (r *Record) write(w Write) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.writes = append(r.writes, w)
	r.mu.Unlock()
}

// read records one read: the ids it returned as hits, and whether it found anything.
func (r *Record) read(hits []string, found bool, searches []string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	if found {
		r.found++
	}
	for _, h := range hits {
		if !slices.Contains(r.hits, h) {
			r.hits = append(r.hits, h)
		}
	}
	r.searches = append(r.searches, searches...)
}

func (r *Record) recall(id string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.recalled = append(r.recalled, id)
	r.mu.Unlock()
}

// ErrDefinedInLevel1 is om.Propose's answer, and the memory tool's harness and propose writes':
// the model defines part of its harness only inside the compaction's level-1 request
// (docs/memory-v2.md, "Self-built harness"); forget and rollback still apply.
var ErrDefinedInLevel1 = errors.New("refused: harness definitions and rules are proposed only inside the compaction's consolidation request, when your harness evidence changed; forget and rollback still apply")

// ErrDefinedToolsRetired is om.Define's answer (docs/code-tool.md, "Named deviation:
// agent-defined tools").
var ErrDefinedToolsRetired = errors.New("refused: defined tools are retired; write a Go function instead (func name(...) ... { ... }), which is kept and called from code in later calls, and see om.Retired() for the queries defined before, with their equivalent snippets")

// errCuratedOff is the error of a read or write that needs the curated store when it is off.
var errCuratedOff = errors.New("curated memory is off (curatedMemory: false), or PiG's config directory could not be found; notes, reflections, history and recall of ids still work")

// errLayerOff is the layer's error without a layer.
var errLayerOff = errors.New("the harness layer is not available here: it needs the per-model harness and curated memory (curatedMemory: true)")

// errBadArgs wraps omwire.ErrBadArgs with the method's name.
func errBadArgs(method string, err error) error {
	return fmt.Errorf("%s: %w (%v)", method, omwire.ErrBadArgs, err)
}

// decode decodes a call's JSON argument array into dst, one pointer per parameter; a call
// may leave trailing parameters out.
func decode(method string, raw json.RawMessage, dst ...any) error {
	var args []json.RawMessage
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return errBadArgs(method, err)
		}
	}
	if len(args) > len(dst) {
		return errBadArgs(method, fmt.Errorf("%d arguments for %d parameters", len(args), len(dst)))
	}
	for i, a := range args {
		if err := json.Unmarshal(a, dst[i]); err != nil {
			return errBadArgs(method, err)
		}
	}
	return nil
}

// Call answers one om call: the child's method and its JSON arguments, the answer as JSON. It
// stops on ctx between rows and pages; an answer given after ctx ended is dropped by the
// supervisor. Call satisfies the sandbox's Handler.
func (c *Call) Call(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	v, err := c.answer(ctx, method, raw)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

func (c *Call) answer(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.detector != nil && !slices.Contains(omwire.DetectorMethods, method) {
		// The supervisor refuses these before asking; the call context holds no write anyway.
		return nil, fmt.Errorf("%s: %w (a detector reads only %v)", method, omwire.ErrBadArgs, omwire.DetectorMethods)
	}
	// A fixed snippet form run as it stands (om.Recall(`<id>`)) passes its placeholder as a
	// value: it is refused, so it never reads as "nothing called <id>" or as a search.
	if p := placeholderArg(raw); p != "" {
		return nil, om.PlaceholderError(p)
	}
	switch method {
	case omwire.MNotes, omwire.MReflections, omwire.MHistory, omwire.MFiles, omwire.MCommits, omwire.MAttempts, omwire.MDropped, omwire.MSaved, omwire.MLayer, omwire.MRetired:
		var o omwire.Options
		if err := decode(method, raw, &o); err != nil {
			return nil, err
		}
		return c.list(ctx, method, o)
	case omwire.MRecall:
		var id string
		var o omwire.Options
		if err := decode(method, raw, &id, &o); err != nil {
			return nil, err
		}
		return c.recallText(ctx, id, o)
	case omwire.MNoteByID:
		var id string
		if err := decode(method, raw, &id); err != nil {
			return nil, err
		}
		return c.noteByID(id)
	case omwire.MReflectionByID:
		var id string
		if err := decode(method, raw, &id); err != nil {
			return nil, err
		}
		return c.reflectionByID(id)
	case omwire.MCitedBy:
		var id string
		if err := decode(method, raw, &id); err != nil {
			return nil, err
		}
		return c.citedBy(ctx, id)
	case omwire.MSupporting:
		var id string
		if err := decode(method, raw, &id); err != nil {
			return nil, err
		}
		return c.supporting(ctx, id)
	case omwire.MSubjects:
		if err := decode(method, raw); err != nil {
			return nil, err
		}
		return c.subjects(ctx)
	case omwire.MInfo:
		if err := decode(method, raw); err != nil {
			return nil, err
		}
		if c.Info == nil {
			return nil, errors.New("the status figures are not available here")
		}
		return c.Info(), nil
	case omwire.MSave:
		var name, typ, description string
		var o omwire.SaveOptions
		if err := decode(method, raw, &name, &typ, &description, &o); err != nil {
			return nil, err
		}
		args := map[string]string{"name": name, "type": typ, "description": description}
		for k, v := range map[string]*string{"body": o.Body, "scope": o.Scope, "quote": o.Quoting} {
			if v != nil {
				args[k] = *v
			}
		}
		return c.curatedWrite(ctx, curated.OpSave, args, Write{Op: WriteSave, Name: name, Type: typ, Description: description})
	case omwire.MEdit:
		var name string
		var o omwire.EditOptions
		if err := decode(method, raw, &name, &o); err != nil {
			return nil, err
		}
		args := map[string]string{"name": name}
		w := Write{Op: WriteEdit, Name: name}
		if o.Type != nil {
			args["type"], w.Type = *o.Type, *o.Type
		}
		if o.Description != nil {
			args["description"], w.Description = *o.Description, *o.Description
		}
		if o.Body != nil {
			args["body"] = *o.Body
		}
		return c.curatedWrite(ctx, curated.OpEdit, args, w)
	case omwire.MForgetMemory:
		var name string
		if err := decode(method, raw, &name); err != nil {
			return nil, err
		}
		return c.curatedWrite(ctx, curated.OpForget, map[string]string{"name": name}, Write{Op: WriteForgetMemory, Name: name})
	case omwire.MFeedback:
		var name, outcome, quote string
		if err := decode(method, raw, &name, &outcome, &quote); err != nil {
			return nil, err
		}
		return c.curatedWrite(ctx, curated.OpFeedback, map[string]string{"name": name, "outcome": outcome, "quote": quote}, Write{Op: WriteFeedback, Name: name})
	case omwire.MLayerForget:
		var kind, name string
		if err := decode(method, raw, &kind, &name); err != nil {
			return nil, err
		}
		if c.Layer == nil {
			return nil, errLayerOff
		}
		return c.landed(ctx, Write{Op: WriteLayerForget, Name: kind + "/" + name}, func() (string, error) { return c.Layer.Forget(kind, name) })
	case omwire.MLayerRollback:
		var n int
		if err := decode(method, raw, &n); err != nil {
			return nil, err
		}
		if c.Layer == nil {
			return nil, errLayerOff
		}
		return c.landed(ctx, Write{Op: WriteLayerRollback, Name: strconv.Itoa(n)}, func() (string, error) { return c.Layer.Rollback(n) })
	case omwire.MPropose:
		var kind, name, text string
		if err := decode(method, raw, &kind, &name, &text); err != nil {
			return nil, err
		}
		return nil, ErrDefinedInLevel1
	case omwire.MDefine:
		var name string
		if err := decode(method, raw, &name); err != nil {
			return nil, err
		}
		return nil, ErrDefinedToolsRetired
	case omwire.MMan:
		if err := decode(method, raw); err != nil {
			return nil, err
		}
		part := ""
		if c.Units != nil {
			part = c.Units.ManualPart()
		}
		return om.Manual(part), nil
	case omwire.MKept:
		var names []string
		if err := decode(method, raw, &names); err != nil {
			return nil, err
		}
		if c.Units == nil {
			return nil, errors.New("kept definitions are not available here")
		}
		return c.Units.Kept(names)
	case omwire.MForget:
		var name string
		if err := decode(method, raw, &name); err != nil {
			return nil, err
		}
		if c.Units == nil {
			return nil, errors.New("kept definitions are not available here")
		}
		return c.Units.Forget(name)
	}
	return nil, fmt.Errorf("%s: %w (no such method)", method, omwire.ErrBadArgs)
}

// landed runs a write whose check passed: it starts only while ctx is live (the last point at
// which a cancel prevents it), then runs to completion whatever happens to the call, and is in
// the record before its answer is sent, so a result built after a cancel or crash lists it.
func (c *Call) landed(ctx context.Context, w Write, run func() (string, error)) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	text, err := run()
	if err != nil {
		return nil, err
	}
	c.Record.write(w)
	return text, nil
}

// curatedWrite runs one curated write through curated.Call.Run, with evidence closures built
// from the branch that skip the memory tool's own calls, so a quote the model wrote into its
// code or strings never confirms anything. A save or an edit tells the user.
func (c *Call) curatedWrite(ctx context.Context, op string, args map[string]string, w Write) (any, error) {
	if c.Curated == nil {
		return nil, errCuratedOff
	}
	call := *c.Curated
	if call.Evidence == nil {
		call.Evidence = func(quote string) bool { return QuotedInConversation(c.Entries, quote, c.Skip) }
	}
	if call.UserEvidence == nil {
		call.UserEvidence = func(quote string) bool { return QuotedByUser(c.Entries, quote, c.Skip) }
	}
	if op == curated.OpForget || op == curated.OpFeedback {
		// The retired defined queries stay stored (an earlier release may share the store), and
		// nothing reads a feedback line on one: ForgetMemory and Feedback act on memories only.
		// A definition is refused by its presence, not its readability, so a stored one that no
		// longer parses (never listed by om.Retired()) is kept too.
		name := args["name"]
		if _, err := call.Store.GetAs(name, call.Project, call.Source); errors.Is(err, curated.ErrNotFound) {
			if _, derr := call.Store.GetDefinition(name); !errors.Is(derr, curated.ErrNotFound) && curated.ValidateName(name) == nil {
				listed := ""
				if derr == nil {
					listed = ", listed by om.Retired()"
				}
				if op == curated.OpFeedback {
					return nil, fmt.Errorf("no memory is called %s; the defined query %s is retired%s, and feedback applies to memories", name, name, listed)
				}
				return nil, fmt.Errorf("no memory is called %s; the defined query %s is retired and stays stored%s", name, name, listed)
			}
		}
	}
	text, err := c.landed(ctx, w, func() (string, error) { return call.Run(op, args) })
	if err == nil && c.Notify != nil && (op == curated.OpSave || op == curated.OpEdit) {
		c.Notify(text.(string))
	}
	return text, err
}

// placeholderArg is the first string among a call's JSON arguments, at any depth (an option's
// search words and field values included), that is a placeholder of the fixed texts; "" when
// none is. Arguments that do not decode are left to the method's own decoding.
func placeholderArg(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	var walk func(any) string
	walk = func(v any) string {
		switch x := v.(type) {
		case string:
			if om.IsPlaceholder(x) {
				return x
			}
		case []any:
			for _, e := range x {
				if p := walk(e); p != "" {
					return p
				}
			}
		case map[string]any:
			// In key order, so the same call is refused with the same text.
			for _, k := range slices.Sorted(maps.Keys(x)) {
				if p := walk(x[k]); p != "" {
					return p
				}
			}
		}
		return ""
	}
	return walk(v)
}
