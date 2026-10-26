package om

import (
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
)

// callError is an om call the extension answered with an error, as the panic a call without
// an error result raises (recovered in the child as a runtime error).
type callError struct {
	method string
	err    error
}

func (e *callError) Error() string { return "om." + e.method + ": " + e.err.Error() }
func (e *callError) Unwrap() error { return e.err }

// call sends one call and wraps its error with the method's name.
func call(method string, out any, args ...any) error {
	if err := omwire.Call(method, out, args...); err != nil {
		return &callError{method: method, err: err}
	}
	return nil
}

// mustCall is call for a function with no error result: an error panics.
func mustCall(method string, out any, args ...any) {
	if err := call(method, out, args...); err != nil {
		panic(err)
	}
}

// ErrNoCall is the error of an om call made with no memory tool call in flight (at replay).
func ErrNoCall() error { return omwire.ErrNoCall() }

// Notes lists the active folded records: the branch's, else the off-path fallback's.
func Notes(opts ...Opt) NoteList {
	var l NoteList
	mustCall(omwire.MNotes, &l, mergeOpts(opts))
	return l
}

// Reflections lists the standing reflections.
func Reflections(opts ...Opt) ReflectionList {
	var l ReflectionList
	mustCall(omwire.MReflections, &l, mergeOpts(opts))
	return l
}

// History lists user, assistant and tool text, newest first, without the current turn.
func History(opts ...Opt) EntryList {
	var l EntryList
	mustCall(omwire.MHistory, &l, mergeOpts(opts))
	return l
}

// Recall reads a record's sources, else an entry (paged), else a saved memory by name.
func Recall(id string, opts ...Opt) (Text, error) {
	var t Text
	err := call(omwire.MRecall, &t, id, mergeOpts(opts))
	return t, err
}

// NoteByID reads one record, dropped or replaced ones included.
func NoteByID(id string) (Note, error) {
	var n Note
	err := call(omwire.MNoteByID, &n, id)
	return n, err
}

// ReflectionByID reads one reflection.
func ReflectionByID(id string) (Reflection, error) {
	var r Reflection
	err := call(omwire.MReflectionByID, &r, id)
	return r, err
}

// Files lists the file records: the newest state per path.
func Files(opts ...Opt) NoteList {
	var l NoteList
	mustCall(omwire.MFiles, &l, mergeOpts(opts))
	return l
}

// Commits lists the commit records.
func Commits(opts ...Opt) NoteList {
	var l NoteList
	mustCall(omwire.MCommits, &l, mergeOpts(opts))
	return l
}

// Attempts lists the attempt records; Failed, Worked and Open select by outcome.
func Attempts(opts ...Opt) NoteList {
	var l NoteList
	mustCall(omwire.MAttempts, &l, mergeOpts(opts))
	return l
}

// Dropped lists the records dropped by rule.
func Dropped(opts ...Opt) NoteList {
	var l NoteList
	mustCall(omwire.MDropped, &l, mergeOpts(opts))
	return l
}

// Subjects counts the active records of each canonical subject.
func Subjects() map[string]int {
	var m map[string]int
	mustCall(omwire.MSubjects, &m)
	return m
}

// Saved lists the curated memories, ranked as the index ranks them.
func Saved(opts ...Opt) MemoryList {
	var l MemoryList
	mustCall(omwire.MSaved, &l, mergeOpts(opts))
	return l
}

// Info is the /om:status figures, each model's labelled with its model.
func Info() StatusInfo {
	var s StatusInfo
	mustCall(omwire.MInfo, &s)
	return s
}

// Layer lists every model's layer definitions, rules included.
func Layer(opts ...Opt) LayerList {
	var l LayerList
	mustCall(omwire.MLayer, &l, mergeOpts(opts))
	return l
}

// Retired lists the retired defined queries with their equivalent snippets.
func Retired(opts ...Opt) RetiredList {
	var l RetiredList
	mustCall(omwire.MRetired, &l, mergeOpts(opts))
	return l
}

// Str is a named string from this call's strings.
func Str(name string) string {
	var s string
	mustCall(omwire.MStr, &s, name)
	return s
}

// Save saves a curated memory, staged unless Quoting gives the user's confirming words.
func Save(name, typ, description string, opts ...SaveOpt) (string, error) {
	var s string
	err := call(omwire.MSave, &s, name, typ, description, mergeSave(opts))
	return s, err
}

// Edit changes a curated memory; it never pins one.
func Edit(name string, opts ...EditOpt) (string, error) {
	var s string
	err := call(omwire.MEdit, &s, name, mergeEdit(opts))
	return s, err
}

// ForgetMemory retires a curated memory.
func ForgetMemory(name string) (string, error) {
	var s string
	err := call(omwire.MForgetMemory, &s, name)
	return s, err
}

// Feedback records that a memory helped or harmed, quoting the conversation.
func Feedback(name, outcome, quote string) (string, error) {
	var s string
	err := call(omwire.MFeedback, &s, name, outcome, quote)
	return s, err
}

// LayerForget forgets one definition from the session model's layer; never a rule.
func LayerForget(kind, name string) (string, error) {
	var s string
	err := call(omwire.MLayerForget, &s, kind, name)
	return s, err
}

// LayerRollback makes the session model's layer what it was at generation gen (0 is before its first change, every seed standing); never a rule.
func LayerRollback(gen int) (string, error) {
	var s string
	err := call(omwire.MLayerRollback, &s, gen)
	return s, err
}

// Propose always refuses: definitions are proposed only at a compaction.
func Propose(kind, name, text string) error {
	return call(omwire.MPropose, nil, kind, name, text)
}

// Define always refuses: a kept Go function replaces a defined tool.
func Define(name string) error { return call(omwire.MDefine, nil, name) }
