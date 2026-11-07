package extract

import (
	"path"
	"strings"
)

// applyPatchSuccess is the line Codex's apply_patch prints when the whole patch applied; a
// result without it is the patch's error.
const applyPatchSuccess = "Success. Updated the following files:"

// patchOf is the patch a call hands Codex's apply_patch: the apply_patch tool's arguments (the
// patch itself, or its input), or a shell command that runs apply_patch on it (argv
// ["apply_patch", patch], ["bash", "-lc", script] or a command string, the script starting with
// apply_patch).
func patchOf(call toolCall) (string, bool) {
	if toolKey(call.name) == "applypatch" {
		if s, ok := call.raw.(string); ok {
			return s, true
		}
		if s, ok := call.args["input"].(string); ok {
			return s, true
		}
		return "", true
	}
	script := ""
	switch c := call.args["command"].(type) {
	case string:
		script = c
	case []any:
		argv := []string{}
		for _, w := range c {
			s, ok := w.(string)
			if !ok {
				return "", false
			}
			argv = append(argv, s)
		}
		switch {
		case len(argv) == 2 && isApplyPatchWord(argv[0]):
			return argv[1], true
		case len(argv) == 3 && path.Base(argv[0]) == "bash" && (argv[1] == "-lc" || argv[1] == "-c"):
			script = argv[2]
		}
	}
	if script == "" {
		script, _ = call.args["cmd"].(string)
	}
	if words := strings.Fields(firstLine(script)); len(words) > 0 && isApplyPatchWord(words[0]) && strings.Contains(script, patchBegin) {
		return script, true
	}
	return "", false
}

// isApplyPatchWord reports the command word that runs apply_patch.
func isApplyPatchWord(w string) bool { return w == "apply_patch" || w == "applypatch" }

// firstLine is text up to its first newline.
func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// Codex's patch markers, each at the start of its line and followed by a path.
const (
	patchBegin  = "*** Begin Patch"
	patchEnd    = "*** End Patch"
	patchAdd    = "*** Add File: "
	patchDelete = "*** Delete File: "
	patchUpdate = "*** Update File: "
	patchMove   = "*** Move to: "
)

// patchPathEvents are the files a patch touches, in its order: written for Add File, edited for
// Update File, deleted for Delete File, and an Update File followed by Move to moves its path (the
// old one moved to the new, the new moved from the old). Only markers between Begin Patch and End
// Patch count; a hunk's lines start with +, -, a space or @@, so none is read as a marker.
func patchPathEvents(patch string) []pathEvent {
	events := []pathEvent{}
	in, update := false, -1
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimRight(line, " \t\r")
		marker := func(prefix string) (string, bool) {
			if !strings.HasPrefix(line, prefix) {
				return "", false
			}
			p := strings.TrimSpace(line[len(prefix):])
			return p, p != ""
		}
		if line == patchBegin {
			in, update = true, -1
			continue
		}
		if !in {
			continue
		}
		moved := update
		update = -1
		if line == patchEnd {
			in = false
		} else if p, ok := marker(patchAdd); ok {
			events = append(events, pathEvent{path: p, state: stateWritten})
		} else if p, ok := marker(patchDelete); ok {
			events = append(events, pathEvent{path: p, state: stateDeleted})
		} else if p, ok := marker(patchUpdate); ok {
			update = len(events)
			events = append(events, pathEvent{path: p, state: stateEdited})
		} else if p, ok := marker(patchMove); ok && moved >= 0 {
			from := events[moved].path
			events[moved] = pathEvent{path: from, state: stateMovedTo, other: p}
			events = append(events, pathEvent{path: p, state: stateMovedFrom, other: from})
		}
	}
	return events
}
