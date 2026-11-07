package extract

import (
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/legacyquery"
	"github.com/drvova/pig-observational-memory/memory/omtool"
	"github.com/drvova/pig-observational-memory/memory/secrets"
	"github.com/drvova/pig-observational-memory/memory/serialize"
)

// The states a file record names (docs/memory-v2.md, "Work facts").
const (
	stateRead      = "read"
	stateWritten   = "written"
	stateEdited    = "edited"
	stateDeleted   = "deleted"
	stateReverted  = "reverted"
	stateMovedTo   = "moved to"
	stateMovedFrom = "moved from"
)

// fileTools are the tools whose path argument names a file, with the state a successful call
// leaves it in, keyed by toolKey so one entry covers every harness's spelling of a name (Claude
// Code's Read and MultiEdit, Kimi's ReadFile, Gemini's read_file). Each name is one a transcript
// reader's fixture or its harness's source shows with a path argument (docs/memory-v2.md, "Work
// facts"). A delete tool is the one unambiguous tombstone a tool gives.
var fileTools = map[string]string{
	"read": stateRead, "readfile": stateRead, "readfiles": stateRead, "view": stateRead,
	"write": stateWritten, "writefile": stateWritten, "createfile": stateWritten, "create": stateWritten,
	"edit": stateEdited, "editfile": stateEdited, "multiedit": stateEdited, "notebookedit": stateEdited, "replace": stateEdited,
	"delete": stateDeleted, "deletefile": stateDeleted, "remove": stateDeleted, "removefile": stateDeleted,
}

// toolKey is the name a tool is looked up by: ASCII lower case, without underscores or hyphens.
func toolKey(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		switch c := name[i]; {
		case c == '_' || c == '-':
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + 'a' - 'A')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// fileToolState is the state a successful call leaves its path in, "" when its tool names no
// file. Kiro's write replacing a string (its command strReplace) edits the file.
func fileToolState(call toolCall) string {
	key := toolKey(call.name)
	if key == "write" && call.args["command"] == "strReplace" {
		return stateEdited
	}
	return fileTools[key]
}

// pathArgs are the arguments that hold a call's path, most common first (notebook_path is Claude
// Code's NotebookEdit's).
var pathArgs = []string{"path", "file_path", "filePath", "notebook_path"}

// exitCode reads a shell result's exit code: "Command exited with code N" (PiG) or "Process
// exited with code N" (Codex's exec_command).
var exitCode = regexp.MustCompile(`(?:Command|Process) exited with code (\d+)`)

// toolCall is one tool call of an assistant message.
type toolCall struct {
	id, name string
	args     map[string]any
	// raw is the call's arguments as the entry holds them: Codex's apply_patch gives a string.
	raw any
}

// toolResult is the result answering a call, found on the branch after it.
type toolResult struct {
	index   int
	text    string
	failed  bool
	present bool
	// details are the result's details, as the entry holds them.
	details any
}

// fileEvent is the newest ordered evidence of a path's state in the range.
type fileEvent struct {
	state, other string
	display      string
	sources      []int
	// reported marks a path PiG reported touched (fileOps), which no tool call ordered: never a
	// receipt.
	reported bool
}

// assistant reads an assistant message from its ordered content blocks: the text before its
// first tool call (text blocks only, never thinking) is the model's own words on that call,
// any other text is a reply, and each call is read for attempts, files, commits and notes.
func (x *extraction) assistant(i int) {
	blocks, _ := x.in.Entries[i].Message["content"].([]any)
	if text, ok := x.in.Entries[i].Message["content"].(string); ok {
		x.reply(i, text)
		return
	}
	before, after := []string{}, []string{}
	calls := []toolCall{}
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if block == nil {
			continue
		}
		switch block["type"] {
		case "text":
			text, _ := block["text"].(string)
			if len(calls) == 0 {
				before = append(before, text)
			} else {
				after = append(after, text)
			}
		case "toolCall":
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			args, _ := block["arguments"].(map[string]any)
			calls = append(calls, toolCall{id: id, name: name, args: args, raw: block["arguments"]})
		}
	}
	words := oneLine(strings.Join(before, "\n"))
	// The words go on the first call that is not memory plumbing; with none, they are a reply.
	first := -1
	for n, call := range calls {
		if !x.isSkipped(call.name) {
			first = n
			break
		}
	}
	if first < 0 {
		x.reply(i, strings.Join(append(before, after...), "\n"))
	} else {
		// The words before a call disagreeing with the user's claim are its pushback, as a reply's
		// would be; they still go on the call.
		anchor, hasAnchor := anchorTime(x.in.Entries[i])
		for _, sentence := range sentences(words) {
			x.pushbackAt(i, sentence, anchor, hasAnchor)
		}
		x.reply(i, strings.Join(after, "\n"))
	}
	for n, call := range calls {
		result := x.resultOf(i, call.id)
		if call.name == omtool.ToolName {
			x.note(i, call, result)
			continue
		}
		if x.isSkipped(call.name) {
			continue
		}
		callWords := ""
		if n == first {
			callWords = words
		}
		x.call(i, call, result, callWords)
	}
}

// resultOf is the tool result answering call id, looked up after the call up to the next user
// message.
func (x *extraction) resultOf(call int, id string) toolResult {
	if id == "" {
		return toolResult{}
	}
	for i := call + 1; i < len(x.in.Entries); i++ {
		entry := x.in.Entries[i]
		if entry.Type != "message" || entry.Message == nil {
			continue
		}
		role := entry.Message["role"]
		if role == "user" {
			break
		}
		if role != "toolResult" || entry.Message["toolCallId"] != id {
			continue
		}
		text := textBlocks(entry.Message["content"])
		failed, _ := entry.Message["isError"].(bool)
		if m := exitCode.FindStringSubmatch(text); m != nil && m[1] != "0" {
			failed = true
		}
		return toolResult{index: i, text: text, failed: failed, present: true, details: entry.Message["details"]}
	}
	return toolResult{}
}

// bashExecution reads a `!command` the user ran into the agent's context as a bash call: it
// can delete, move or revert files and commit like the model's own. A `!!command` is kept out
// of the context by PiG, so memory keeps it out too.
func (x *extraction) bashExecution(i int) {
	msg := x.in.Entries[i].Message
	if excluded, _ := msg["excludeFromContext"].(bool); excluded {
		return
	}
	command, _ := msg["command"].(string)
	output, _ := msg["output"].(string)
	failed := false
	switch code := msg["exitCode"].(type) {
	case float64:
		failed = code != 0
	case int:
		failed = code != 0
	}
	if cancelled, _ := msg["cancelled"].(bool); cancelled {
		failed = true
	}
	x.call(i, toolCall{name: "bash", args: map[string]any{"command": command}}, toolResult{index: i, text: output, failed: failed, present: true}, "")
}

// shellCommand is a call's shell command: its "command" argument, else "cmd" (Codex's
// exec_command).
func shellCommand(args map[string]any) (string, bool) {
	if command, ok := args["command"].(string); ok {
		return command, true
	}
	command, ok := args["cmd"].(string)
	return command, ok
}

// callTarget is what a call acted on: its command, else its path (resolved, for matching, and
// as shown), else its arguments, with any secret in them redacted (secrets.Redact), so a
// record's target never holds one and a later call on the same command still matches it.
func (x *extraction) callTarget(call toolCall) (key, display string) {
	key, display = x.rawCallTarget(call)
	return secrets.Redact(key), secrets.Redact(display)
}

func (x *extraction) rawCallTarget(call toolCall) (key, display string) {
	if command, ok := shellCommand(call.args); ok && strings.TrimSpace(command) != "" {
		return strings.TrimSpace(command), strings.TrimSpace(command)
	}
	for _, arg := range pathArgs {
		if p, ok := call.args[arg].(string); ok && strings.TrimSpace(p) != "" {
			return x.pathKey(p), x.pathDisplay(p)
		}
	}
	if patch, ok := patchOf(call); ok {
		keys, shown := []string{}, []string{}
		for _, e := range patchPathEvents(patch) {
			if e.state != stateMovedFrom {
				keys, shown = append(keys, x.pathKey(e.path)), append(shown, x.pathDisplay(e.path))
			}
		}
		if len(keys) > 0 {
			return strings.Join(keys, " "), strings.Join(shown, " ")
		}
	}
	args := serialize.JSONStringify(call.args)
	return args, args
}

// attemptKey identifies the calls one attempt continues: the identical command, or the same
// tool on the same path.
func attemptKey(tool, target string) string { return tool + "\x00" + target }

// call reads one tool call with its result: an attempt record when the call failed, has no
// result, carries the model's words, or resolves an earlier failed or open attempt; the file
// state a successful call leaves; a commit.
func (x *extraction) call(i int, call toolCall, result toolResult, words string) {
	sources := []int{i}
	if result.present && result.index != i {
		sources = append(sources, result.index)
	}
	key, display := x.callTarget(call)
	outcome := ledger.OutcomeWorked
	switch {
	case !result.present:
		outcome = ledger.OutcomeOpen
	case result.failed:
		outcome = ledger.OutcomeFailed
	}
	// A patch applied only when apply_patch says so; any other result is its error.
	patch, isPatch := patchOf(call)
	if isPatch && outcome == ledger.OutcomeWorked && !strings.Contains(result.text, applyPatchSuccess) {
		outcome = ledger.OutcomeFailed
	}
	akey := attemptKey(call.name, key)
	earlier := x.unresolved[akey]
	// A worked call is a receipt (receipts.go); one that would close a plan is recorded even
	// with no words of the model's, so its closing link has a record to sit on.
	var rc *receipt
	if outcome == ledger.OutcomeWorked {
		rc = x.callReceipt(call, key, result.text)
	}
	if result.present {
		x.outputs = append(x.outputs, callOutput{target: key, text: result.text})
	}
	if outcome != ledger.OutcomeWorked || words != "" || len(earlier) > 0 || x.wouldClose(rc, i) {
		line := attemptLine(call.name, display, outcome, result.text)
		obs := ledger.Observation{Kind: ledger.KindAttempt, Outcome: outcome, Tool: call.name, Target: key}
		x.callKeys(&obs.Keys, call, outcome, result.text)
		// Only a success resolves the attempts it continues; each failure keeps its own record,
		// since its reason is a specific of what was tried. The success carries the model's
		// words from the attempts it resolves, so dropping them loses none.
		said := []string{}
		if outcome == ledger.OutcomeWorked {
			obs.Supersedes = append(obs.Supersedes, earlier...)
			for _, id := range earlier {
				if w := x.words[id]; w != "" && !slices.Contains(said, w) {
					said = append(said, w)
				}
			}
		}
		if words != "" && !slices.Contains(said, words) {
			said = append(said, words)
		}
		obs.Content = line
		if len(said) > 0 {
			obs.Content = strings.Join(said, " ") + " [" + line + "]"
		}
		r := x.add(obs, sources...)
		r.obs.ID = recordID(r.obs)
		r.receipt = rc
		x.words[r.obs.ID] = strings.Join(said, " ")
		if outcome == ledger.OutcomeWorked {
			for _, id := range earlier {
				x.superseded[id] = true
			}
			delete(x.unresolved, akey)
		} else {
			x.unresolved[akey] = append(x.unresolved[akey], r.obs.ID)
		}
	}
	x.outputRecords(call, result, key, display, sources)
	if outcome != ledger.OutcomeWorked {
		return
	}
	if isPatch {
		for _, event := range patchPathEvents(patch) {
			x.fileEvent(event.path, event.state, event.other, sources)
		}
	}
	if state := fileToolState(call); state != "" {
		for _, arg := range pathArgs {
			if p, ok := call.args[arg].(string); ok && strings.TrimSpace(p) != "" {
				x.fileEvent(p, state, "", sources)
				if state == stateWritten || state == stateEdited {
					x.revision(i, call, p)
				}
				break
			}
		}
	}
	if command, ok := shellCommand(call.args); ok {
		for _, event := range shellPathEvents(command) {
			x.fileEvent(event.path, event.state, event.other, sources)
		}
		if hash, subject, ok := commitFrom(command, result.text); ok {
			r := x.add(ledger.Observation{Content: "commit " + hash + " " + subject, Kind: ledger.KindCommit, Target: hash}, sources...)
			r.receipt = &receipt{kind: ledger.ReceiptCommit, keys: append([]string{hash}, ledger.RecordKeys(subject)...)}
		}
	}
}

// callKeys adds the keys level 0 knows of a call (contracts/encoding.md): its shell command, else
// the paths it names (resolved, as Target holds them), and a failure's first line.
func (x *extraction) callKeys(keys *ledger.Keys, call toolCall, outcome, resultText string) {
	if command, ok := shellCommand(call.args); ok && strings.TrimSpace(command) != "" {
		keys.Add(ledger.KeyCommands, strings.TrimSpace(command))
	} else if p, ok := callPath(call); ok {
		keys.Add(ledger.KeyPaths, x.pathKey(p))
	} else if patch, ok := patchOf(call); ok {
		for _, e := range patchPathEvents(patch) {
			if e.state != stateMovedFrom {
				keys.Add(ledger.KeyPaths, x.pathKey(e.path))
			}
		}
	}
	if outcome == ledger.OutcomeFailed {
		keys.Add(ledger.KeyErrors, clip(failureLine(resultText), serialize.BriefKeyArgRunes))
	}
}

// callPath is a call's path argument (pathArgs), the first non-blank one.
func callPath(call toolCall) (string, bool) {
	for _, arg := range pathArgs {
		if p, ok := call.args[arg].(string); ok && strings.TrimSpace(p) != "" {
			return p, true
		}
	}
	return "", false
}

// attemptLine is an attempt record's line: the tool, what it acted on and the outcome, with
// the first line saying why when it failed.
func attemptLine(tool, display, outcome, resultText string) string {
	line := tool + " " + strconv.Quote(clip(display, serialize.BriefKeyArgRunes)) + " " + outcome
	if outcome == ledger.OutcomeFailed {
		if reason := failureLine(resultText); reason != "" {
			line += ": " + clip(reason, serialize.BriefKeyArgRunes)
		}
	}
	return line
}

// CallLine is level 0's record line for one tool call and its result (docs/memory-v2.md,
// "Work facts"): the tool, what it acted on (its command, else its path shown relative to
// cwd, else its arguments) and its outcome, failed (isError or a nonzero exit, with the first
// line saying why) or worked. It is the line an attempt record carries, picked from the call,
// never reworded.
func CallLine(cwd, tool string, args map[string]any, resultText string, isError bool) string {
	x := &extraction{in: Input{Cwd: cwd}}
	_, display := x.callTarget(toolCall{name: tool, args: args})
	outcome := ledger.OutcomeWorked
	if m := exitCode.FindStringSubmatch(resultText); isError || (m != nil && m[1] != "0") {
		outcome = ledger.OutcomeFailed
	}
	return attemptLine(tool, display, outcome, resultText)
}

// attemptWords are the model's own words an attempt record carries before its line ("words
// [tool "target" outcome]"), "" when it carries none.
func attemptWords(o ledger.Observation) string {
	if !strings.HasSuffix(o.Content, "]") || o.Tool == "" {
		return ""
	}
	if i := strings.LastIndex(o.Content, " ["+o.Tool+" \""); i > 0 {
		return o.Content[:i]
	}
	return ""
}

// failureLine is the first line of a failed result that says why: the first non-empty line
// that is not the shell's exit-code note.
func failureLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || exitCode.MatchString(line) {
			continue
		}
		return line
	}
	return ""
}

// note reads a memory tool call: a save or edit that landed is a note the session model wrote,
// recorded with origin model and its description as written. A later note under the same name
// supersedes the earlier one. A code call is read from its result's details (omtool.Details'
// writes), whatever its error flag: a save that landed before a panic, a cancel or a crash is
// still a note. A call written before the code tool parses its query with the frozen recognizer,
// and only when its result did not fail.
func (x *extraction) note(i int, call toolCall, result toolResult) {
	if !result.present {
		return
	}
	if _, isCode, query := omtool.CallArgs(call.args); !isCode {
		if result.failed {
			return
		}
		parsed, err := legacyquery.Parse(query)
		if err != nil || (parsed.Write != legacyquery.WriteSave && parsed.Write != legacyquery.WriteEdit) {
			return
		}
		x.addNote(i, result, parsed.Args["name"], parsed.Args["description"])
		return
	}
	d := omtool.DetailsFrom(result.details)
	if d == nil {
		return
	}
	for _, w := range d.Writes {
		switch w.Op {
		case omtool.WriteSave, omtool.WriteEdit:
			x.addNote(i, result, w.Name, w.Description)
		case omtool.WriteCorrect:
			x.addCorrection(i, result, w.Name, w.Description, w.Type == omtool.CorrectQuoted)
		}
	}
}

// addCorrection records om.Correct's correction of the record id (docs/memory-v2.md,
// "Correcting a record"), never an edit of it: on the user's quoted words a correction with the
// user's authority that supersedes it, else one with the model's that only possibly updates it,
// which the three-way rule (ledger.Supersession) weighs beside it.
func (x *extraction) addCorrection(i int, result toolResult, id, text string, quoted bool) {
	text = strings.TrimSpace(text)
	target, ok := x.in.Ledger.ObservationsByID[id]
	if !ok || text == "" {
		return
	}
	obs := ledger.Observation{Content: oneLine(text), Kind: "correction", Origin: ledger.OriginModel, Subject: target.Subject, Scope: target.Scope,
		Source: ledger.SourceAssistantClaim}
	if quoted {
		obs.Source, obs.Supersedes = ledger.SourceUserSaid, []string{id}
	} else {
		obs.PossiblyUpdates = []string{id}
	}
	r := x.add(obs, i, result.index)
	r.obs.ID = recordID(r.obs)
	if quoted {
		x.superseded[id] = true
	}
}

// addNote records the note a save or edit of name wrote, superseding the earlier one under it.
func (x *extraction) addNote(i int, result toolResult, name, description string) {
	description = strings.TrimSpace(description)
	if name == "" || description == "" {
		return
	}
	obs := ledger.Observation{Content: oneLine(description), Kind: ledger.KindNote, Origin: ledger.OriginModel, Target: name, Subject: ledger.SubjectInText(description, x.subjects, x.in.Ledger.Aliases)}
	obs.Supersedes = append(obs.Supersedes, x.notes[name]...)
	r := x.add(obs, i, result.index)
	r.obs.ID = recordID(r.obs)
	for _, id := range x.notes[name] {
		x.superseded[id] = true
	}
	x.notes[name] = []string{r.obs.ID}
}

// pathKey is a path resolved against the working directory, the key one file is matched by.
func (x *extraction) pathKey(p string) string {
	p = strings.TrimSpace(p)
	if x.in.Cwd != "" && !filepath.IsAbs(p) && !strings.HasPrefix(p, "~") {
		p = filepath.Join(x.in.Cwd, p)
	}
	return filepath.Clean(p)
}

// pathDisplay is a path as shown: relative to the working directory when inside it.
func (x *extraction) pathDisplay(p string) string {
	key := x.pathKey(p)
	if x.in.Cwd != "" {
		if rel, err := filepath.Rel(filepath.Clean(x.in.Cwd), key); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return rel
		}
	}
	return key
}

// fileEvent records ordered evidence of a path's state; the newest wins, except that a read
// never replaces a change.
func (x *extraction) fileEvent(p, state, other string, sources []int) {
	key := x.pathKey(p)
	previous, seen := x.fileEvents[key]
	if state == stateRead && (len(x.files[key]) > 0 || (seen && previous.state != stateRead)) {
		return
	}
	if !seen {
		x.fileOrder = append(x.fileOrder, key)
	}
	if other != "" {
		other = x.pathDisplay(other)
	}
	x.fileEvents[key] = fileEvent{state: state, other: other, display: x.pathDisplay(p), sources: sources}
}

// fileOps adds the paths PiG reported touched that no ordered evidence and no record gave a
// state: the set is unordered, so it never replaces one. Each cites the first entry of the
// range that names the path, else the range's last source entry.
func (x *extraction) fileOps(last int) {
	ops := append([]FileOp{}, x.in.FileOps.Ops...)
	sort.SliceStable(ops, func(a, b int) bool { return ops[a].Path < ops[b].Path })
	for _, op := range ops {
		key := x.pathKey(op.Path)
		if _, seen := x.fileEvents[key]; seen || len(x.files[key]) > 0 || strings.TrimSpace(op.Path) == "" {
			continue
		}
		source := last
		for i := max(min(x.in.FileOps.From, x.in.Start), 0); i <= last; i++ {
			if mentions(x.in.Entries[i].Message, op.Path) {
				source = i
				break
			}
		}
		x.fileOrder = append(x.fileOrder, key)
		x.fileEvents[key] = fileEvent{state: op.State, display: x.pathDisplay(op.Path), sources: []int{source}, reported: true}
	}
}

// mentions reports a message whose tool calls name path.
func mentions(message map[string]any, path string) bool {
	if message == nil || message["role"] != "assistant" {
		return false
	}
	blocks, _ := message["content"].([]any)
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if block == nil || block["type"] != "toolCall" {
			continue
		}
		if strings.Contains(serialize.JSONStringify(block["arguments"]), path) {
			return true
		}
	}
	return false
}

// emitFiles appends one file record per path at its newest state, superseding the path's
// earlier file record: the files read as a newest-per-path list with tombstones.
func (x *extraction) emitFiles() {
	for _, key := range x.fileOrder {
		event := x.fileEvents[key]
		content := event.display + " " + event.state
		if event.other != "" {
			content += " " + event.other
		}
		obs := ledger.Observation{Content: content, Kind: ledger.KindFile, Target: key}
		obs.Keys.Add(ledger.KeyPaths, key)
		for _, id := range x.files[key] {
			if !slices.Contains(obs.Supersedes, id) {
				obs.Supersedes = append(obs.Supersedes, id)
			}
		}
		r := x.add(obs, event.sources...)
		// A file written or edited by a tool call is a write receipt on its path.
		if !event.reported && (event.state == stateWritten || event.state == stateEdited) {
			r.receipt = &receipt{kind: ledger.ReceiptWrite, keys: []string{key}}
		}
	}
	// Records are in branch order: file records sit at their newest evidence.
	sort.SliceStable(x.records, func(a, b int) bool { return x.records[a].anchor < x.records[b].anchor })
}
