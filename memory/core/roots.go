package core

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
)

// EvidenceRoot is the digest of the evidence a quote from tool output rests on (decision 7): the
// file it came from when the read names one (path, relative to project when under it), else the
// quoted words themselves. It is never the tool result's bytes: the same file read with `cat -n`,
// `head`, a grep match or behind a tool's header is one root, and another file quoting the same
// line is another. Sessions, record text and the reading tool never enter it.
func EvidenceRoot(project, path, quote string) string {
	path = OneLine(path)
	path = strings.TrimPrefix(path, "./")
	if project != "" && strings.HasPrefix(path, project+"/") {
		path = path[len(project)+1:]
	}
	var sum [32]byte
	if path != "" {
		sum = sha256.Sum256([]byte("file\x00" + project + "\x00" + path))
	} else {
		sum = sha256.Sum256([]byte("text\x00" + NormKey(quote)))
	}
	return hex.EncodeToString(sum[:])
}

// UserRoot is the evidence root of a user's turn: the entry it was said in and the words.
func UserRoot(entryID, text string) string {
	sum := sha256.Sum256([]byte("user\x00" + entryID + "\x00" + NormKey(text)))
	return hex.EncodeToString(sum[:])
}

// readCommands are the shell commands whose last plain word is the file they read.
var readCommands = []string{"cat", "head", "tail", "nl", "less", "more", "bat", "sed", "grep", "rg", "awk"}

// looksLikePath reports a word that names a file: it holds a dot or a slash.
func looksLikePath(w string) bool { return strings.ContainsAny(w, "./") }

// CommandPath is the file a shell read command names: its last word that is no flag, when the
// command is one of readCommands; "" otherwise.
func CommandPath(command string) string {
	words := fields(command)
	if len(words) < 2 || !slices.Contains(readCommands, words[0]) {
		return ""
	}
	for i := len(words) - 1; i > 0; i-- {
		w := strings.Trim(words[i], `"'`)
		if w == "" || strings.HasPrefix(w, "-") {
			continue
		}
		if looksLikePath(w) {
			return w
		}
		return ""
	}
	return ""
}

// GrepPath is the file a grep match line names ("path:12:text" or "path-12-text"), on the first
// line of text holding quote; "" when that line has none.
func GrepPath(text, quote string) string {
	needle := NormKey(quote)
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(NormKey(line), needle) {
			continue
		}
		i := 0
		for i < len(line) && line[i] != ':' && !isSpace(line[i]) {
			i++
		}
		if i == 0 || i >= len(line) || line[i] != ':' {
			return ""
		}
		path := line[:i]
		j := i + 1
		for j < len(line) && line[j] >= '0' && line[j] <= '9' {
			j++
		}
		if j == i+1 || j >= len(line) || (line[j] != ':' && line[j] != '-') || !looksLikePath(path) {
			return ""
		}
		return path
	}
	return ""
}

// ModelFamily is a model key's family: the leading letters of its id (after the last "/"),
// ASCII lowercased ("anthropic/claude-opus-5" is "claude", "openai/gpt-5" "gpt"); the lowercased
// id when it starts with none, "" for no model.
func ModelFamily(model string) string {
	id := lowerASCII(model[strings.LastIndex(model, "/")+1:])
	i := 0
	for i < len(id) && id[i] >= 'a' && id[i] <= 'z' {
		i++
	}
	if i == 0 {
		return id
	}
	return id[:i]
}

// Hint is the note memory one model wrote carries when shown to another (the per-model hint
// rule): "written by a <family> model", "" when the writer is unknown or of the reader's family.
func Hint(writerModel, readerModel string) string {
	w := ModelFamily(writerModel)
	if w == "" || w == ModelFamily(readerModel) {
		return ""
	}
	return "written by a " + w + " model"
}

// Value is the word a value stripped from a lesson becomes.
const Value = "<value>"

// isValueWord reports a word that is a value: a digit, one of _ / \ @ = : . # $ %, or a
// lowercase letter followed by an uppercase one (camelCase).
func isValueWord(w string) bool {
	for i := 0; i < len(w); i++ {
		c := w[i]
		switch {
		case c >= '0' && c <= '9', strings.IndexByte(`_/\@=:.#$%`, c) >= 0:
			return true
		case i > 0 && c >= 'A' && c <= 'Z' && w[i-1] >= 'a' && w[i-1] <= 'z':
			return true
		}
	}
	return false
}

// StripValues is a lesson without its values (decision 8), the form in which alone it crosses
// projects: each code span, and each word holding a value (isValueWord, its leading brackets
// and quotes and trailing punctuation kept), becomes Value, and adjacent values become one.
func StripValues(text string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(text, '`')
		if i < 0 {
			break
		}
		j := strings.IndexByte(text[i+1:], '`')
		if j < 0 || strings.IndexByte(text[i+1:i+1+j], '\n') >= 0 {
			break
		}
		b.WriteString(text[:i])
		b.WriteString(Value)
		text = text[i+2+j:]
	}
	b.WriteString(text)
	var out []string
	for _, w := range fields(b.String()) {
		lead := 0
		for lead < len(w) && strings.IndexByte(`([{"'`, w[lead]) >= 0 {
			lead++
		}
		trail := len(w)
		for trail > lead && strings.IndexByte(`.,;:!?)]}"'`, w[trail-1]) >= 0 {
			trail--
		}
		if core := w[lead:trail]; core != Value && core != "" && isValueWord(core) {
			w = w[:lead] + Value + w[trail:]
		}
		if w == Value && len(out) > 0 && out[len(out)-1] == Value {
			continue
		}
		out = append(out, w)
	}
	return strings.Join(out, " ")
}
