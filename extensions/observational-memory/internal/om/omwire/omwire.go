// Package omwire is the om package's transport (docs/code-tool.md, "The domain API"): the one
// call function every om stub and every value method that calls back by id goes through, and
// the wire shapes of the options those calls carry.
//
// Nothing here is ever extracted into an interpreter symbol table, so interpreted code cannot
// name Install. The sandbox's child main installs the transport once, before its first
// evaluation; in any other process nothing installs one, and every call returns ErrNoCall.
package omwire

import (
	"errors"
	"sync"
)

// Transport answers one om call: method names it, args are its arguments in order, and the
// answer is decoded into out (nil when the call returns nothing).
type Transport func(method string, out any, args ...any) error

var (
	installMu sync.Mutex
	installed Transport
)

// errNoCall is what a call returns when no call is in flight to answer it.
var errNoCall = errors.New("om: no memory tool call is in flight to answer this (definitions are replayed with no call)")

// ErrNoCall is the error a call gets when no call is in flight: in any process but the sandbox's
// child, and in the child while it replays saved definitions.
func ErrNoCall() error { return errNoCall }

// Install sets the transport. It may be called once; a second call panics, so nothing can
// replace the transport after the child installed it.
func Install(t Transport) {
	installMu.Lock()
	defer installMu.Unlock()
	if installed != nil {
		panic("omwire: transport already installed")
	}
	if t == nil {
		panic("omwire: nil transport")
	}
	installed = t
}

// Call sends one call through the installed transport, or returns ErrNoCall when none is.
func Call(method string, out any, args ...any) error {
	installMu.Lock()
	t := installed
	installMu.Unlock()
	if t == nil {
		return errNoCall
	}
	return t(method, out, args...)
}

// The methods om calls. Every stub and value method names one of these; the host answers only
// the ones its method table for the child's kind lists. Str is answered inside the child.
const (
	MNotes          = "Notes"
	MReflections    = "Reflections"
	MHistory        = "History"
	MRecall         = "Recall"
	MNoteByID       = "NoteByID"
	MReflectionByID = "ReflectionByID"
	MFiles          = "Files"
	MCommits        = "Commits"
	MAttempts       = "Attempts"
	MDropped        = "Dropped"
	MSubjects       = "Subjects"
	MSaved          = "Saved"
	MInfo           = "Info"
	MLayer          = "Layer"
	MRetired        = "Retired"
	MCitedBy        = "CitedBy"
	MSupporting     = "Supporting"
	MSave           = "Save"
	MEdit           = "Edit"
	MForgetMemory   = "ForgetMemory"
	MFeedback       = "Feedback"
	MLayerForget    = "LayerForget"
	MLayerRollback  = "LayerRollback"
	MPropose        = "Propose"
	MDefine         = "Define"
	MStr            = "Str"
	// The seeded store functions of the namespace: man's store part, kept and forget.
	MMan    = "man"
	MKept   = "kept"
	MForget = "forget"
)

// Methods are every method om and the seeded functions call, Str included.
var Methods = []string{
	MNotes, MReflections, MHistory, MRecall, MNoteByID, MReflectionByID, MFiles, MCommits,
	MAttempts, MDropped, MSubjects, MSaved, MInfo, MLayer, MRetired, MCitedBy, MSupporting,
	MSave, MEdit, MForgetMemory, MFeedback, MLayerForget, MLayerRollback, MPropose, MDefine,
	MStr, MMan, MKept, MForget,
}

// Pair is one field and value of a Where or Contains option.
type Pair struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// Options are a read's options as they cross the pipe: every om.Opt of the read merged in
// order. Scalars set later win; Search, Where and Contains accumulate. A pointer is nil when
// the option was not given.
type Options struct {
	Search    []string `json:"search,omitempty"`
	From      string   `json:"from,omitempty"`
	Through   string   `json:"through,omitempty"`
	Since     string   `json:"since,omitempty"`
	Until     string   `json:"until,omitempty"`
	ByEvent   bool     `json:"by_event,omitempty"`
	AsOf      bool     `json:"as_of,omitempty"`
	Where     []Pair   `json:"where,omitempty"`
	Contains  []Pair   `json:"contains,omitempty"`
	Subject   string   `json:"subject,omitempty"`
	Status    string   `json:"status,omitempty"`
	SortField string   `json:"sort_field,omitempty"`
	SortDesc  bool     `json:"sort_desc,omitempty"`
	Limit     *int     `json:"limit,omitempty"`
	Offset    *int     `json:"offset,omitempty"`
	Failed    bool     `json:"failed,omitempty"`
	Worked    bool     `json:"worked,omitempty"`
	Open      bool     `json:"open,omitempty"`
}

// SaveOptions are om.Save's options.
type SaveOptions struct {
	Body    *string `json:"body,omitempty"`
	Scope   *string `json:"scope,omitempty"`
	Quoting *string `json:"quoting,omitempty"`
}

// EditOptions are om.Edit's options.
type EditOptions struct {
	Type        *string `json:"type,omitempty"`
	Description *string `json:"description,omitempty"`
	Body        *string `json:"body,omitempty"`
}

// ErrBadArgs is wrapped by the host's answer to a call whose arguments do not decode into its
// method's parameters. The child's stubs always send what decodes, so such a call means code
// corrupted the child: the supervisor kills it (docs/code-tool.md, "The protocol").
var ErrBadArgs = errors.New("the arguments do not decode into the method's parameters")
