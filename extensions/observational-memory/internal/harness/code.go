package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/codepolicy"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/codestore"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/legacyquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omhost"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omtool"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/provenance"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/sandbox"
)

// The memory tool's execution (docs/code-tool.md): each call's code is checked by the code
// policy in the extension, run in the namespace child (one per extension process, reset at
// session_start), its om calls answered by omhost from a snapshot of the branch, the
// declarations it compiled kept in the per-project code store, and one ModelKindCode usage
// event recorded from its Details. Nothing here makes a model request, and nothing the output
// says depends on the session model.

// Namespace is the extension process's namespace child and the code store it loads from.
type Namespace struct {
	once sync.Once
	// sup is the supervisor, set once by supervisor(); Reset and Status read it without
	// making one.
	sup atomic.Pointer[sandbox.Supervisor]

	mu sync.Mutex
	// dir is the code store the live namespace loads from, "" for none (no config root).
	dir string
	// seen are the digests the live namespace loaded or kept since it started; types the
	// sources of the types it defines (kept units and types compiled since it started).
	seen  map[string]string
	types map[string]string
	// started counts the starts that loaded units (tests).
	started int
}

// loadLive sets seen and types to what a child started now loads from the store: the units()
// state, also taken when the child is killed, so a call made before the next child starts is
// checked against the namespace it will run in rather than the one that died.
func (n *Namespace) loadLive() (codestore.Loaded, bool) {
	store, ok := n.store()
	var l codestore.Loaded
	loaded := false
	if ok {
		if got, err := store.Load(); err == nil {
			l, loaded = got, true
		}
	}
	n.mu.Lock()
	n.seen, n.types = map[string]string{}, map[string]string{}
	if loaded {
		n.seen = l.Digests()
		for _, u := range l.Units {
			if u.Kind == codepolicy.UnitType {
				n.types[u.Name] = u.Src
			}
		}
	}
	n.mu.Unlock()
	return l, loaded
}

// kill kills the namespace child and takes the state the next one starts with.
func (n *Namespace) kill() {
	if sup := n.sup.Load(); sup != nil {
		sup.Kill()
	}
	n.loadLive()
}

func (n *Namespace) supervisor() *sandbox.Supervisor {
	n.once.Do(func() {
		n.sup.Store(sandbox.New(sandbox.Config{Mode: sandbox.ModeNamespace, Methods: sandbox.NamespaceMethods(), Units: n.units}))
	})
	return n.sup.Load()
}

// store is the code store the live namespace loads from; false without one.
func (n *Namespace) store() (codestore.Store, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return codestore.Store{Dir: n.dir}, n.dir != ""
}

// units is the supervisor's Units: the store loaded and checked at every child start. The
// live namespace's seen digests and types start over from what it loads.
func (n *Namespace) units() []sandbox.Component {
	n.mu.Lock()
	n.started++
	n.mu.Unlock()
	l, ok := n.loadLive()
	if !ok {
		return nil
	}
	out := make([]sandbox.Component, len(l.Components))
	for i, c := range l.Components {
		for _, u := range c.Units {
			out[i].Units = append(out[i].Units, sandbox.Unit{Name: u.Name, Src: u.Src})
		}
		out[i].Deps = c.Deps
	}
	return out
}

// bind points the namespace at the code store dir: a namespace that loaded another store (a
// session in another project) is killed, so the next call starts from this one.
func (n *Namespace) bind(dir string) {
	n.mu.Lock()
	changed := n.dir != dir
	n.dir = dir
	n.mu.Unlock()
	if changed {
		n.kill()
	}
}

// Reset kills the namespace child (session_start, session_shutdown): the next call starts a new
// one from the kept definitions, so only they reach the next conversation.
func (n *Namespace) Reset() { n.kill() }

// Status is the supervisor's recorded state; it starts, kills or replays nothing.
func (n *Namespace) Status() sandbox.Status {
	if sup := n.sup.Load(); sup != nil {
		return sup.Status()
	}
	return sandbox.Status{}
}

// snapshot copies the seen digests and the types.
func (n *Namespace) snapshot() (map[string]string, map[string]string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	seen := make(map[string]string, len(n.seen))
	for k, v := range n.seen {
		seen[k] = v
	}
	types := make(map[string]string, len(n.types))
	for k, v := range n.types {
		types[k] = v
	}
	return seen, types
}

// problems are the supervisor's load errors and quarantined units, for kept().
func (n *Namespace) problems() []codestore.Problem {
	st := n.Status()
	var out []codestore.Problem
	for _, e := range st.LoadErrors {
		out = append(out, codestore.Problem{Units: e.Units, Msg: e.Msg})
	}
	for _, e := range st.Quarantined {
		out = append(out, codestore.Problem{Units: e.Units, Msg: "quarantined: " + e.Msg})
	}
	return out
}

// errNoCodeStore is kept's and forget's error without a config root.
var errNoCodeStore = errors.New("no code store: PiG's config directory could not be found, so definitions live only for this conversation")

// units is omhost.Units for one call: kept and the manual's part read the store, and forget
// removes from it and asks for the live namespace to follow when the call returns.
type units struct {
	n *Namespace
	// declared are the names the call's code declares (codepolicy.Program.Units), which forget
	// refuses: the call keeps them when it returns, so a forget of one in the same call would
	// leave the store and the live namespace disagreeing.
	declared []string
	// stub and restart are what forget asked of the live namespace.
	mu      sync.Mutex
	stubs   []string
	restart bool
}

func (u *units) Kept(names []string) (string, error) {
	store, ok := u.n.store()
	if !ok {
		return "", errNoCodeStore
	}
	l, err := store.Load()
	if err != nil {
		return "", storeError(err)
	}
	seen, _ := u.n.snapshot()
	return codestore.Kept(l, u.n.problems(), seen, names...), nil
}

func (u *units) Forget(name string) (string, error) {
	store, ok := u.n.store()
	if !ok {
		return "", errNoCodeStore
	}
	for _, d := range u.declared {
		if d == name || strings.HasPrefix(d, name+".") {
			return "", fmt.Errorf("%s is not forgotten: this call declares %s, and a call keeps what it declares when it returns; forget it in a call that does not declare it", name, d)
		}
	}
	res, err := store.Forget(name)
	if err != nil {
		return "", storeError(err)
	}
	u.mu.Lock()
	if res.Restart {
		u.restart = true
	} else if res.Stub != "" {
		u.stubs = append(u.stubs, res.Stub)
	}
	u.mu.Unlock()
	u.n.mu.Lock()
	delete(u.n.seen, name)
	u.n.mu.Unlock()
	return res.Text(), nil
}

func (u *units) ManualPart() string {
	text, err := u.Kept(nil)
	if err != nil {
		return err.Error()
	}
	return text
}

// storeError is a code store error as the child and the result see it: an I/O error's path is
// left out (docs/code-tool.md, "the child never learns its path"); /om:status, which runs in the
// extension, shows the whole error.
func storeError(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("the code store is unreadable or unwritable: %s: %v", pe.Op, pe.Err)
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return fmt.Errorf("the code store is unwritable: %s: %v", le.Op, le.Err)
	}
	return err
}

// CodeResult is a memory tool call's result.
type CodeResult struct {
	Text    string
	IsError bool
	Details omtool.Details
}

// requestContext is the tool call's cancellation (sdkhost.Host.RequestContext): PiG cancels it
// on the user's abort and at session end. A host without one never cancels.
func requestContext(h host.ToolHost) context.Context {
	if rc, ok := h.(interface{ RequestContext() context.Context }); ok {
		if ctx := rc.RequestContext(); ctx != nil {
			return ctx
		}
	}
	return context.Background()
}

// codeStoreDir is the code store of h's project, "" without a config root.
func codeStoreDir(h host.Host) string {
	homer, ok := h.(host.ConfigHomer)
	if !ok || homer.ConfigHome() == "" {
		return ""
	}
	return codestore.Dir(homer.ConfigHome(), ProjectRoot(h.Cwd()))
}

// stringsArg reads the strings argument: an object of names to strings.
func stringsArg(v any) (map[string]string, error) {
	if v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		if s, ok := v.(map[string]string); ok {
			return s, nil
		}
		return nil, errors.New("strings must be an object of names to text values")
	}
	out := make(map[string]string, len(m))
	for k, raw := range m {
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("strings[%q] is not a text value", k)
		}
		out[k] = s
	}
	return out, nil
}

// RunMemory runs one memory tool call (docs/code-tool.md, "The tool"): params carries the code
// and the optional strings (a call written before the code tool carries a query, read as its
// code). The user is notified of every save and edit.
func (m *Manager) RunMemory(h host.ToolHost, params map[string]any) CodeResult {
	code, isCode, query := omtool.CallArgs(params)
	if !isCode {
		code = query
	}
	d := omtool.Details{Code: code, Outcome: omtool.OutcomeOK}
	strs, err := stringsArg(params[omtool.ParamStrings])
	if err != nil {
		d.Outcome = omtool.OutcomeCompile
		return CodeResult{Text: "Error: " + err.Error(), IsError: true, Details: d}
	}
	for name := range strs {
		d.Strings = append(d.Strings, name)
	}
	sort.Strings(d.Strings)
	ns := m.namespace()
	ns.bind(codeStoreDir(h))
	u := &units{n: ns}
	entries, err := h.GetBranch()
	if err != nil {
		d.Outcome = omtool.OutcomeCompile
		return CodeResult{Text: "Error: " + err.Error(), IsError: true, Details: d}
	}
	call, on := m.resolve(h)
	if strings.TrimSpace(code) == "" {
		d.Manual = true
		res := CodeResult{Text: om.Manual(u.ManualPart()), Details: d}
		m.recordCode(h, call, on, entries, d)
		return res
	}
	seen, types := ns.snapshot()
	p, perr := codepolicy.Check(codepolicy.Input{Code: code, Strings: strs, Types: types})
	if perr != nil {
		var syntax *codepolicy.SyntaxError
		if errors.As(perr, &syntax) {
			if text, ok := legacyAnswer(code); ok {
				d.Outcome, d.Legacy = omtool.OutcomeCompile, true
				m.recordCode(h, call, on, entries, d)
				return CodeResult{Text: text, IsError: true, Details: d}
			}
		}
		d.Outcome = omtool.OutcomeCompile
		m.recordCode(h, call, on, entries, d)
		return CodeResult{Text: "Error: " + perr.Error() + manualFor(perr.Error()), IsError: true, Details: d}
	}
	all, skipped := p.Units()
	for _, unit := range all {
		u.declared = append(u.declared, unit.Name)
	}
	for _, unit := range skipped {
		u.declared = append(u.declared, unit.Name)
	}
	if p.LastExpr && printsOnly(p.Chunks[len(p.Chunks)-1].Src) {
		// fmt.Print's byte count and nil error are not the code's value.
		p.LastExpr = false
	}
	rec := &omhost.Record{}
	oc := &omhost.Call{
		Entries:        entries,
		SessionEntries: sessionEntries(h),
		Skip:           m.SkipNames(),
		Notify:         func(message string) { h.Notify(message, "info") },
		Units:          u,
		Record:         rec,
	}
	if m.Info != nil {
		oc.Info = m.Info(h)
	}
	if on {
		c := call
		oc.Curated = &c
		oc.Retired = func() ([]om.RetiredQuery, error) { return RetiredQueries(call.Store) }
		oc.Layer = OMLayer(h, call.Store, ModelKey(h.SessionModel()))
	}
	ctx := requestContext(h)
	res := ns.supervisor().Run(ctx, sandbox.Request{Program: p, Strings: strs}, oc)
	recorded := rec.Snapshot()
	if res.Outcome == sandbox.OutcomeCompile && len(p.Chunks) == 1 && recorded.Reads == 0 && len(recorded.Writes) == 0 {
		// A legacy query that is also Go (notes | count) does not compile; nothing ran.
		if text, ok := legacyAnswer(code); ok {
			d.Outcome, d.Legacy = omtool.OutcomeCompile, true
			m.recordCode(h, call, on, entries, d)
			return CodeResult{Text: text, IsError: true, Details: d}
		}
	}
	d.Outcome = res.Outcome
	for _, w := range recorded.Writes {
		d.Writes = append(d.Writes, omtool.Write(w))
	}
	d.Hits, d.Recalled, d.Searches, d.Empty = recorded.Hits, recorded.Recalled, recorded.Searches, recorded.Empty
	var lines []string
	if res.Outcome != sandbox.OutcomeCancelled && res.Outcome != sandbox.OutcomeCrashed {
		lines = m.keep(ns, p, res, entries, strs, seen, &d)
	}
	lines = append(lines, ns.follow(ctx, u, res)...)
	text := codeText(res, lines, d.Writes)
	m.recordCode(h, call, on, entries, d)
	return CodeResult{Text: text, IsError: res.Outcome != sandbox.OutcomeOK, Details: d}
}

// keep stores the units of the declaration chunks the child reported compiled, after the
// provenance check, and returns the result lines: one naming every unit kept, one per unit
// refused and one per variable, which is never kept.
func (m *Manager) keep(ns *Namespace, p codepolicy.Program, res sandbox.Result, entries []ledger.Entry, strs map[string]string, seen map[string]string, d *omtool.Details) []string {
	all, skipped := p.Units()
	var compiled []codepolicy.Unit
	for _, u := range all {
		if u.Chunk < len(res.Compiled) && res.Compiled[u.Chunk] == 'c' {
			compiled = append(compiled, u)
		}
	}
	var lines []string
	if len(compiled) > 0 {
		store, ok := ns.store()
		if !ok {
			for _, u := range compiled {
				d.Refused = append(d.Refused, u.Name)
				lines = append(lines, "not kept: "+u.Name+": "+errNoCodeStore.Error())
			}
		} else {
			own := []string{om.Manual("")}
			if l, err := store.Load(); err == nil {
				for _, u := range l.Units {
					own = append(own, u.Src)
				}
			}
			kept, err := store.Keep(compiled, provenance.Code(entries, strs, own...), seen)
			if err != nil {
				for _, u := range compiled {
					d.Refused = append(d.Refused, u.Name)
				}
				lines = append(lines, "not kept: the code store could not be written: "+storeError(err).Error())
			} else {
				ns.mu.Lock()
				for _, u := range kept.Kept {
					if ns.seen != nil {
						ns.seen[u.Name] = u.Digest
					}
					d.Kept = append(d.Kept, u.Name)
				}
				ns.mu.Unlock()
				for _, r := range kept.Refused {
					d.Refused = append(d.Refused, r.Name)
				}
				lines = append(lines, kept.Lines()...)
			}
		}
		// The types the live namespace now defines, kept or not, so a redefinition with
		// another source is refused.
		ns.mu.Lock()
		for _, u := range compiled {
			if u.Kind == codepolicy.UnitType && ns.types != nil {
				ns.types[u.Name] = u.Src
			}
		}
		ns.mu.Unlock()
	}
	for _, s := range skipped {
		lines = append(lines, "not kept: "+s.Name+": "+s.Reason)
	}
	return lines
}

// follow brings the live namespace in line with what forget did during the call: a forgotten
// function is shadowed by its refusing stub, and anything else restarts the namespace.
func (n *Namespace) follow(ctx context.Context, u *units, res sandbox.Result) []string {
	u.mu.Lock()
	stubs, restart := u.stubs, u.restart
	u.mu.Unlock()
	if res.Restarted || (len(stubs) == 0 && !restart) {
		return nil
	}
	if !restart {
		for _, stub := range stubs {
			p, err := codepolicy.Check(codepolicy.Input{Code: stub})
			if err != nil {
				restart = true
				break
			}
			if r := n.supervisor().Run(ctx, sandbox.Request{Program: p}, sandbox.HandlerFunc(noCalls)); r.Outcome != sandbox.OutcomeOK {
				restart = true
				break
			}
		}
	}
	if restart {
		n.kill()
		return []string{"The namespace restarted from the kept definitions: unsaved variables are gone."}
	}
	return nil
}

// noCalls answers no om call: a forget stub makes none.
func noCalls(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("no om call is answered here")
}

// printsOnly reports whether expression src is a call of fmt.Print, Printf or Println, whose
// results (the bytes written and a nil error) the result does not show as the last value.
func printsOnly(src string) bool {
	e, err := parser.ParseExpr(src)
	if err != nil {
		return false
	}
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "fmt" && (sel.Sel.Name == "Print" || sel.Sel.Name == "Printf" || sel.Sel.Name == "Println")
}

// omSymbol finds an om symbol, or a builtin the interpreter lacks, a compile error names.
var omSymbol = regexp.MustCompile(`\bom\.([A-Z][A-Za-z0-9]*)|undefined: (min|max|clear)\b`)

// manualFor is the manual section matching an om symbol msg names, "" for none.
func manualFor(msg string) string {
	m := omSymbol.FindStringSubmatch(msg)
	if m == nil {
		return ""
	}
	name := m[1]
	if name == "" {
		name = m[2]
	}
	if s := om.ManualSection(name); s != "" {
		return "\n\n" + s
	}
	return ""
}

// legacyRemoved opens the answer to a query of the removed language.
const legacyRemoved = "The memory query language was removed: the memory tool runs Go code, and nothing of this query ran. Empty code prints the manual."

// legacyAnswer is the answer to code that parses as a query of the removed language
// (legacyquery, decision 9): the removal notice and, for a read, the equivalent snippet as text,
// generated by the strict formatter; for a write, the manual section of the om write that
// replaces it. false when code is not such a query. Nothing is run.
func legacyAnswer(code string) (string, bool) {
	q, err := legacyquery.Parse(code)
	if err != nil {
		return "", false
	}
	if q.Write != "" {
		_, reason := legacyquery.HintOf(q)
		text := legacyRemoved + "\nThis was a " + q.Write + " write; " + strings.TrimPrefix(reason, "it is a write ("+q.Write+"); ") + "."
		if s := om.ManualSection(legacyWriteSymbol[q.Write]); s != "" {
			text += "\n\n" + s
		}
		return text, true
	}
	snippet, reason := legacyquery.HintOf(q)
	if snippet == "" {
		return legacyRemoved + "\nNo snippet replaces it: " + reason + ".", true
	}
	return legacyRemoved + "\nRun this code instead:\n" + snippet, true
}

// legacyWriteSymbol is the om symbol whose manual section a legacy write is answered with.
var legacyWriteSymbol = map[string]string{
	legacyquery.WriteSave:     "Save",
	legacyquery.WriteEdit:     "Edit",
	legacyquery.WriteForget:   "ForgetMemory",
	legacyquery.WriteFeedback: "Feedback",
	legacyquery.WriteDefine:   "Define",
	legacyquery.WritePropose:  "Propose",
	legacyquery.WriteHarness:  "LayerForget",
	legacyquery.WriteRollback: "LayerRollback",
}

// codeText is the result text (docs/code-tool.md, "Result"): what the code printed, the last
// value, the error, then the kept and refused lines and the namespace notices; for a cancelled
// or crashed call, the writes that landed before it. The whole is cut to one recall page.
func codeText(res sandbox.Result, lines []string, writes []omtool.Write) string {
	var b strings.Builder
	b.WriteString(res.Out)
	addLine := func(s string) {
		if s == "" {
			return
		}
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
		b.WriteString(s)
	}
	if res.HasVal {
		addLine(res.Val)
	}
	switch res.Outcome {
	case sandbox.OutcomeCompile, sandbox.OutcomeRuntime:
		addLine("Error: " + res.Err + manualFor(res.Err))
	case sandbox.OutcomeCancelled, sandbox.OutcomeCrashed:
		addLine(res.Outcome + ": " + res.Err)
		if res.Restarted && !strings.Contains(res.Err, "unsaved variables are gone") {
			addLine("The namespace restarts from the kept definitions, so unsaved variables are gone.")
		}
		if len(writes) > 0 {
			var ws []string
			for _, w := range writes {
				ws = append(ws, w.Op+" "+w.Name)
			}
			addLine("Writes that landed before the " + map[string]string{sandbox.OutcomeCancelled: "cancel", sandbox.OutcomeCrashed: "crash"}[res.Outcome] + ": " + strings.Join(ws, ", "))
		}
	}
	cut := res.Cut
	for _, l := range lines {
		addLine(l)
	}
	text := b.String()
	if n := utf8.RuneCountInString(text); n > om.RecallPageChars {
		cut += n - om.RecallPageChars
		text = string([]rune(text)[:om.RecallPageChars])
	}
	if cut > 0 {
		text += fmt.Sprintf("\n[cut: %d more characters]", cut)
	}
	if strings.TrimSpace(text) == "" {
		return "(no output)"
	}
	return text
}

// recordCode records one ModelKindCode event in the session model's usage ledger
// (docs/code-tool.md, "Replacing the query language"): compile, runtime and crashed as an error,
// cancelled as nothing, a write as ok, every read empty as empty, otherwise the hits classified
// against what the summary and index show; a recall of a shown record uses the summary rather
// than repeating it, so it counts as ok.
func (m *Manager) recordCode(h host.ToolHost, call curated.Call, on bool, entries []ledger.Entry, d omtool.Details) {
	_, l, counted := profileOf(h, call, on)
	if !counted || d.Outcome == omtool.OutcomeCancelled {
		return
	}
	event := curated.ModelEvent{Kind: curated.ModelKindCode, Legacy: d.Legacy}
	switch {
	case d.Outcome != omtool.OutcomeOK:
		event.Outcome = curated.OutcomeError
	case len(d.Writes) > 0:
		event.Outcome = curated.OutcomeOK
	case d.Empty:
		event.Outcome = curated.OutcomeEmpty
	default:
		var indexShown []string
		if m.IndexShown != nil {
			indexShown = m.IndexShown()
		}
		event.Outcome, event.Target = VisibilityOf(entries, indexShown).Classify(d.Hits)
		if len(d.Recalled) > 0 && event.Outcome == curated.OutcomeShown {
			event.Outcome, event.Target = curated.OutcomeOK, ""
		}
	}
	l.Record(event)
}

// SkipNames are the tools whose calls are memory plumbing (MemoryToolNames' source): the memory
// tool, the defined tools this process registered, and the retired defined queries' names no
// other tool holds, so old defined-tool calls on the branch stay plumbing whatever is
// registered. The list only grows within a process.
func (m *Manager) SkipNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := []string{}
	for name := range m.registered {
		names = append(names, name)
	}
	for name := range m.retired {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return append([]string{omtool.ToolName}, slices.DeleteFunc(names, func(n string) bool { return n == omtool.ToolName })...)
}

// CodeStatusLines are the memory tool's /om:status lines (docs/code-tool.md, "Replacing the
// query language"): the kept definitions by state, with the units the load-time check refused,
// and the namespace child's recorded state (generation, starts, last crash, load errors,
// quarantined units, whether it made itself the out-of-memory killer's first pick). They read
// the code store and the supervisor's record only: no child starts, dies or replays.
func (m *Manager) CodeStatusLines(h host.Host) []string {
	var lines []string
	dir := codeStoreDir(h)
	if dir == "" {
		lines = append(lines, "Kept definitions: no code store (PiG's config directory was not found)")
	} else if l, err := (codestore.Store{Dir: dir}).Load(); err != nil {
		lines = append(lines, "Kept definitions: the code store is unreadable ("+err.Error()+"); nothing loads and nothing is written until it is fixed")
	} else {
		line := fmt.Sprintf("Kept definitions: %d", len(l.Units))
		if len(l.Refused) > 0 {
			var names []string
			for _, r := range l.Refused {
				names = append(names, r.Name+" ("+r.Reason+")")
			}
			line += fmt.Sprintf(", %d refused at load: %s", len(l.Refused), strings.Join(names, "; "))
		}
		lines = append(lines, line)
	}
	st := m.namespace().Status()
	if st.Starts == 0 {
		return append(lines, "Namespace: not started in this process")
	}
	live := "not running (the next call starts it)"
	if st.Live {
		live = "running"
	}
	lines = append(lines, fmt.Sprintf("Namespace: generation %d, %s, %d starts", st.Generation, live, st.Starts))
	if st.OOM != "" {
		lines = append(lines, "Out-of-memory preference: "+st.OOM)
	}
	for _, e := range st.LoadErrors {
		lines = append(lines, "Load error: "+strings.Join(e.Units, ", ")+": "+e.Msg)
	}
	for _, e := range st.Quarantined {
		lines = append(lines, "Quarantined: "+strings.Join(e.Units, ", ")+": "+e.Msg)
	}
	if st.LastCrash != "" {
		crash, _, _ := strings.Cut(st.LastCrash, "\n")
		lines = append(lines, "Last crash: "+crash)
	}
	return lines
}
