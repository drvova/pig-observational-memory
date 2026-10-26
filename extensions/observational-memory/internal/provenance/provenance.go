// Package provenance is the copied-segment check every text the model wants kept passes
// (docs/code-tool.md, "Provenance"): text from files, web pages or tool output never becomes a
// definition or a kept code unit. It reads the branch, so it runs only where a branch exists:
// at proposal and keep time, never at load.
//
// The check compares words, not characters: a run of MinCopiedWords consecutive words of a text
// found in output on the branch (a tool result, a custom message, a summary, a `!command` run:
// output the model read, not words it wrote) is a quotation, wherever it sits in the text.
// This is a heuristic against verbatim copying of injected text, not a guarantee:
// concatenation or escapes get past it.
//
// The package imports only ledger, serialize and the standard library, so rules (for layer
// definitions) and the keep path of the memory tool (for code units) both call it.
package provenance

import (
	"go/scanner"
	"go/token"
	"strconv"
	"strings"
	"unicode"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

// MinCopiedWords is how many consecutive words of a text make a quotation: a run of that many
// found in the branch's output is refused wherever it sits in the text (inside a line, between
// the model's own words). Shorter runs (a query, a delimiter, a field name) are common words,
// and are checked only as the whole text where a check compares the whole.
const MinCopiedWords = 6

// Outputs are the texts on branch the model read rather than wrote (tool results, custom
// messages, summaries, `!command` runs), each normalized (Normalize).
func Outputs(branch []ledger.Entry) []string {
	out := []string{}
	for _, entry := range branch {
		var haystack string
		switch {
		case entry.Type == "message" && entry.Message != nil && entry.Message["role"] == "bashExecution":
			// A `!command` run keeps its command and output in their own fields, not content.
			haystack = serialize.BashExecutionText(entry.Message)
		case entry.Type == "message" && entry.Message != nil && entry.Message["role"] != "user" && entry.Message["role"] != "assistant":
			haystack = blockText(entry.Message["content"])
		case entry.Type == "custom_message":
			haystack = blockText(entry.Content)
		case entry.Type == "branch_summary" || entry.Type == "compaction":
			haystack, _ = entry.Summary.(string)
		default:
			continue
		}
		if haystack = Normalize(haystack); haystack != "" {
			out = append(out, haystack)
		}
	}
	return out
}

// blockText is a message content's text blocks.
func blockText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		parts := []string{}
		for _, raw := range c {
			if block, ok := raw.(map[string]any); ok && block["type"] == "text" {
				if text, ok := block["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// Normalize is text with whitespace collapsed and case ignored, as the checks compare it.
func Normalize(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

// Contained reports whether needle (normalized) appears in any of haystacks.
func Contained(haystacks []string, needle string) bool {
	if needle == "" {
		return false
	}
	for _, h := range haystacks {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}

// CopiedFromOutput reports whether text, whitespace collapsed and case ignored, appears whole
// in output on branch.
func CopiedFromOutput(branch []ledger.Entry, text string) bool {
	return Contained(Outputs(branch), Normalize(text))
}

// CopiedSegment reports the first part of text found in output on branch: the whole text, or
// any run of MinCopiedWords consecutive words (CopiedRun), so a quotation is found inside a line
// or a table value as well as on its own. A part that is also in seed (the shipped text of the
// name) is the kernel's own, not a quotation.
func CopiedSegment(branch []ledger.Entry, text, seed string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	outputs := Outputs(branch)
	if len(outputs) == 0 {
		return "", false
	}
	normalizedSeed := Normalize(seed)
	if whole := Normalize(text); !(normalizedSeed != "" && strings.Contains(normalizedSeed, whole)) && Contained(outputs, whole) {
		return text, true
	}
	return CopiedRun(outputs, text, seed)
}

// QuotedFromOutput reports the first run of MinCopiedWords consecutive words of text found in
// output on branch (CopiedRun), the whole text aside: the check for a text short enough to be
// common words as a whole, such as a query, which may well match one the branch's output
// already shows.
func QuotedFromOutput(branch []ledger.Entry, text string) (string, bool) {
	outputs := Outputs(branch)
	if strings.TrimSpace(text) == "" || len(outputs) == 0 {
		return "", false
	}
	return CopiedRun(outputs, text, "")
}

// Words are text's words as the quotation check compares them: lowercased, split at every
// character that is not a letter or a digit, so a word glued to a table's delimiter or quote
// ("thin-chunk,ignore", "\"always") is still its own word.
func Words(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// CopiedRun reports the first run of MinCopiedWords consecutive words of text (Words) found in
// outputs, a run also in seed excluded.
func CopiedRun(outputs []string, text, seed string) (string, bool) {
	seedWords := " " + strings.Join(Words(seed), " ") + " "
	words := Words(text)
	runs := map[string]bool{}
	firsts := map[string]bool{}
	for i := 0; i+MinCopiedWords <= len(words); i++ {
		run := strings.Join(words[i:i+MinCopiedWords], " ")
		if !strings.Contains(seedWords, " "+run+" ") {
			runs[run] = true
			firsts[words[i]] = true
		}
	}
	if len(runs) == 0 {
		return "", false
	}
	// Every run of the text is looked up while each output's words are scanned once.
	for _, output := range outputs {
		outWords := Words(output)
		for i := 0; i+MinCopiedWords <= len(outWords); i++ {
			if !firsts[outWords[i]] {
				continue
			}
			if run := strings.Join(outWords[i:i+MinCopiedWords], " "); runs[run] {
				return run, true
			}
		}
	}
	return "", false
}

// CodeError is a code unit the provenance check refused, with the quoted run.
type CodeError struct {
	// Quoted is the run of MinCopiedWords words found in output.
	Quoted string
}

func (e *CodeError) Error() string {
	return "refused (provenance): the unit quotes text found in tool output, a file or another message on this branch (" + strconv.Quote(clip(e.Quoted, 80)) + "); a kept definition must be your own code"
}

// Code returns the provenance check for code units kept in one call (the check
// codestore.Keep takes): a unit whose source, any of its string literals (unquoted, so an
// escape such as \n between two words does not hide them) or any of its comments contains a
// run of MinCopiedWords words found in output on branch is refused. The call's strings values
// count as output, since a unit that embeds one copies text the model did not write into code
// that replays in every later session. A run also found in one of own (texts that are already
// the extension's or already kept, such as the stored units' sources, so a unit the model read
// back with kept and changed is not a quotation of itself) is not a quotation.
//
// Only runs are compared, never the whole source: a short unit such as `const N = 3` may well
// match a line the branch's output shows. The outputs are read once, when Code is called.
func Code(branch []ledger.Entry, strs map[string]string, own ...string) func(src string) error {
	outputs := Outputs(branch)
	for _, v := range strs {
		if n := Normalize(v); n != "" {
			outputs = append(outputs, n)
		}
	}
	seed := strings.Join(own, "\n")
	return func(src string) error {
		if len(outputs) == 0 {
			return nil
		}
		for _, text := range append([]string{src}, codeTexts(src)...) {
			if quoted, ok := CopiedRun(outputs, text, seed); ok {
				return &CodeError{Quoted: quoted}
			}
		}
		return nil
	}
}

// codeTexts are src's string literals, unquoted, and its comments, in order.
func codeTexts(src string) []string {
	var s scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	s.Init(file, []byte(src), nil, scanner.ScanComments)
	var out []string
	for {
		_, tok, lit := s.Scan()
		switch tok {
		case token.EOF:
			return out
		case token.STRING, token.CHAR:
			if v, err := strconv.Unquote(lit); err == nil {
				out = append(out, v)
			}
		case token.COMMENT:
			out = append(out, lit)
		}
	}
}

// clip cuts s to n characters, with an ellipsis when cut.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
