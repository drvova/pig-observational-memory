package extract

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/secrets"
	"github.com/drvova/pig-observational-memory/memory/serialize"
)

// Tool output (docs/memory-v2.md, "Tool output"): level 0 reads a tool's result by code into
// two record shapes. A value record (ledger.KindValue) is one exact value the output printed,
// one per value kind and raw spelling per session, with its first and latest sighting and how
// many results printed it. An evidence record (ledger.KindEvidence) is at most
// maxEvidenceLines verbatim lines of a result that failed or matched a failure grammar. The
// scanners are hand-written over ASCII character classes (a byte of 0x80 or more is one class,
// "other", so a scan reads the same on bytes and on characters) with fixed caps, so the Lisp
// port (lisp/src/output.lisp) is a transliteration.

// The value kinds, in the order the summary lists them (ValueKinds).
const (
	ValuePath    = "path"
	ValueError   = "error"
	ValueSHA     = "sha"
	ValueURL     = "url"
	ValueKV      = "kv"
	ValueIP      = "ip"
	ValueIssue   = "issue"
	ValueTest    = "test"
	ValueBranch  = "branch"
	ValueSymbol  = "symbol"
	ValueVersion = "version"
)

// ValueKinds are the value kinds in the summary's order.
var ValueKinds = []string{ValuePath, ValueError, ValueSHA, ValueURL, ValueKV, ValueIP, ValueIssue, ValueTest, ValueBranch, ValueSymbol, ValueVersion}

// The caps of one tool result (docs/memory-v2.md, "Tool output").
const (
	maxEvidenceLines     = 8
	maxEvidenceLineChars = 160
	maxNewValues         = 20
	maxURLChars          = 120
	maxValueChars        = 160
	// EvidenceSeparator joins an evidence record's lines on its one line.
	EvidenceSeparator = " ⏎ "
)

// The classes of a call, from its tool and command words (outputClass).
const (
	classNone = iota
	classTest
	classBuild
	classSearch
	classGit
)

// outputValue is one value a result printed.
type outputValue struct {
	kind, value string
}

// facts are what one tool result gives: its values, distinct, in extractor order, and its
// evidence lines (failed says a failure grammar matched).
type facts struct {
	values   []outputValue
	evidence []string
	shapes   map[string]bool
	seen     map[string]bool
	failed   bool
}

func (f *facts) value(kind, value string) {
	if value == "" || utf8.RuneCountInString(value) > maxValueChars {
		return
	}
	key := kind + ":" + value
	if f.seen[key] {
		return
	}
	// A value the secret scanner would redact is never indexed (contracts/secrets.md).
	if secrets.Redact(value) != value {
		return
	}
	f.seen[key] = true
	f.values = append(f.values, outputValue{kind: kind, value: value})
}

// line adds an evidence line: noise stripped (evidenceLine), at most maxEvidenceLines, a line
// whose shape (digit runs masked) an earlier one has left out.
func (f *facts) line(text string) {
	text = evidenceLine(text)
	if text == "" || len(f.evidence) >= maxEvidenceLines {
		return
	}
	shape := lineShape(text)
	if f.shapes[shape] {
		return
	}
	f.shapes[shape] = true
	f.evidence = append(f.evidence, text)
}

// lockfiles are the files whose reads give no values: their hashes and versions are not what
// the session worked on.
var lockfiles = []string{"go.sum", "package-lock.json", "poetry.lock", "Cargo.lock", "yarn.lock", "pnpm-lock.yaml", "composer.lock", "Gemfile.lock", "uv.lock", "bun.lockb"}

// outputFactsOf reads one call's result (docs/memory-v2.md, "Tool output"): the extractors run
// in order (test runners, build and lint diagnostics, search, git, stack traces, the generic
// value scan), each adding values and evidence lines; a file read gives values only, a lockfile
// read nothing. failed is the result's own failure (isError or a nonzero exit).
func outputFactsOf(call toolCall, text string, failed bool) facts {
	f := facts{shapes: map[string]bool{}, seen: map[string]bool{}}
	state := fileToolState(call)
	switch state {
	case stateWritten, stateEdited, stateDeleted:
		return f
	}
	if state == stateRead {
		for _, arg := range pathArgs {
			if p, ok := call.args[arg].(string); ok && isLockfile(p) {
				return f
			}
		}
	}
	lines := outputLines(text)
	class := outputClass(call)
	reading := state == stateRead || class == classSearch
	if !reading {
		testFacts(&f, lines)
		diagnosticFacts(&f, lines, class == classTest || class == classBuild)
	}
	if class == classSearch {
		searchFacts(&f, call)
	}
	if class == classGit {
		gitFacts(&f, lines)
	}
	if !reading {
		stackFacts(&f, lines)
		markerFacts(&f, lines)
	}
	for _, line := range lines {
		scanValues(&f, line, class == classGit)
	}
	if failed && len(f.evidence) == 0 {
		fallbackEvidence(&f, lines)
	}
	if !failed && !f.failed {
		f.evidence = nil
	}
	return f
}

func isLockfile(p string) bool {
	base := p
	if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
		base = base[i+1:]
	}
	for _, name := range lockfiles {
		if base == name {
			return true
		}
	}
	return false
}

// --- character classes (ASCII; a byte of 0x80 or more is "other")

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }
func isLower(c byte) bool { return c >= 'a' && c <= 'z' }
func isAlpha(c byte) bool { return isUpper(c) || isLower(c) }
func isAlnum(c byte) bool { return isAlpha(c) || isDigit(c) }
func isIdent(c byte) bool { return isAlnum(c) || c == '_' }
func isHex(c byte) bool   { return isDigit(c) || (c >= 'a' && c <= 'f') }

// isBlank is ASCII white space: space, tab, and the other controls up to 0x20.
func isBlank(c byte) bool { return c <= ' ' || c == 0x7f }

// trimBlank trims ASCII white space (isBlank) from both ends.
func trimBlank(s string) string {
	start, end := 0, len(s)
	for start < end && isBlank(s[start]) {
		start++
	}
	for end > start && isBlank(s[end-1]) {
		end--
	}
	return s[start:end]
}

// lowerASCII lower-cases ASCII letters only.
func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if isUpper(c) {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// --- noise

// outputLines are a result's lines with the noise removed: ANSI escapes stripped, a line's
// carriage-return overwrites resolved to the text after the last one, and progress, download
// and binary lines dropped.
func outputLines(text string) []string {
	text = stripANSI(text)
	out := []string{}
	for _, line := range strings.Split(text, "\n") {
		for strings.HasSuffix(line, "\r") {
			line = line[:len(line)-1]
		}
		if i := strings.LastIndexByte(line, '\r'); i >= 0 {
			line = line[i+1:]
		}
		if noiseLine(line) {
			continue
		}
		out = append(out, line)
	}
	return out
}

// stripANSI removes ANSI escape sequences: CSI (ESC [ params intermediates final), OSC (ESC ]
// up to BEL or ESC \) and any other ESC with the one character after it.
func stripANSI(s string) string {
	if strings.IndexByte(s, 0x1b) < 0 {
		return s
	}
	var b strings.Builder
	n := len(s)
	for i := 0; i < n; i++ {
		c := s[i]
		if c != 0x1b {
			b.WriteByte(c)
			continue
		}
		if i+1 >= n {
			break
		}
		switch s[i+1] {
		case '[':
			j := i + 2
			for j < n && s[j] >= 0x30 && s[j] <= 0x3f {
				j++
			}
			for j < n && s[j] >= 0x20 && s[j] <= 0x2f {
				j++
			}
			if j < n && s[j] >= 0x40 && s[j] <= 0x7e {
				j++
			}
			i = j - 1
		case ']':
			j := i + 2
			for j < n && s[j] != 0x07 && (s[j] != 0x1b || j+1 >= n || s[j+1] != '\\') {
				j++
			}
			switch {
			case j < n && s[j] == 0x07:
				j++
			case j < n:
				j += 2
			}
			i = j - 1
		default:
			i++
		}
	}
	return b.String()
}

// progressPrefixes open a progress or download line.
var progressPrefixes = []string{"Downloading ", "Downloaded ", "Collecting ", "Fetching ", "Resolving ", "Receiving objects", "Counting objects", "Compressing objects", "Writing objects", "Unpacking objects", "Enumerating objects", "remote: Counting", "remote: Compressing", "remote: Enumerating", "remote: Total", "Progress: "}

// barRunes draw progress bars.
const barRunes = "█▓▒░■━"

// noiseLine reports a progress, download or binary line.
func noiseLine(line string) bool {
	t := trimBlank(line)
	if t == "" {
		return false
	}
	if percentStart(t) || hasRate(t) || strings.Contains(t, "=====>") || strings.Contains(t, "[######") {
		return true
	}
	for _, p := range progressPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	run := 0
	for _, r := range t {
		if strings.ContainsRune(barRunes, r) {
			run++
			if run >= 6 {
				return true
			}
		} else {
			run = 0
		}
	}
	return binaryLine(t)
}

// percentStart reports a line opening with up to three digits, an optional decimal part and %.
func percentStart(t string) bool {
	i := 0
	for i < len(t) && i < 3 && isDigit(t[i]) {
		i++
	}
	if i == 0 {
		return false
	}
	if i < len(t) && t[i] == '.' {
		j := i + 1
		for j < len(t) && isDigit(t[j]) {
			j++
		}
		if j > i+1 {
			i = j
		}
	}
	return i < len(t) && t[i] == '%'
}

// hasRate reports a transfer rate: a number, an optional space and kB/s, MB/s, MiB/s, GB/s...
func hasRate(t string) bool {
	from := 0
	for {
		k := strings.Index(t[from:], "B/s")
		if k < 0 {
			return false
		}
		k += from
		from = k + 1
		j := k - 1
		if j >= 0 && t[j] == 'i' {
			j--
		}
		if j < 0 || strings.IndexByte("kKMG", t[j]) < 0 {
			continue
		}
		j--
		if j >= 0 && t[j] == ' ' {
			j--
		}
		if j >= 0 && isDigit(t[j]) {
			return true
		}
	}
}

// binaryLine reports a binary or base64 blob: over 400 characters with no space, or a run of
// 200 base64 characters.
func binaryLine(t string) bool {
	if strings.IndexByte(t, ' ') < 0 && utf8.RuneCountInString(t) > 400 {
		return true
	}
	run := 0
	for i := 0; i < len(t); i++ {
		c := t[i]
		if isAlnum(c) || c == '+' || c == '/' || c == '=' {
			run++
			if run >= 200 {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}

// evidenceLine is a line as evidence keeps it: durations, timestamps and progress counters
// removed, a role-like label quoted (serialize.EscapeRoleLabels: tool text is never a turn), on
// one line, clipped to maxEvidenceLineChars; "" for a blank line or the shell's
// exit-code note.
func evidenceLine(line string) string {
	line = trimBlank(line)
	if line == "" || exitCode.MatchString(line) {
		return ""
	}
	return clip(serialize.EscapeRoleLabels(stripTimes(line)), maxEvidenceLineChars)
}

// stripTimes removes from a line what makes equal lines look different: a parenthesized
// duration ("(0.02s)", "(12 ms)"), " in 0.12s", an ISO timestamp and a "[ 52%]" counter.
func stripTimes(line string) string {
	var b strings.Builder
	n := len(line)
	for i := 0; i < n; {
		if j := durationAt(line, i); j > i {
			i = j
			continue
		}
		if j := timestampAt(line, i); j > i {
			i = j
			continue
		}
		if j := percentCounterAt(line, i); j > i {
			i = j
			continue
		}
		b.WriteByte(line[i])
		i++
	}
	return trimBlank(b.String())
}

// number reads digits with an optional decimal part at i: the end, or i when none.
func number(s string, i int) int {
	j := i
	for j < len(s) && isDigit(s[j]) {
		j++
	}
	if j == i {
		return i
	}
	if j < len(s) && s[j] == '.' {
		k := j + 1
		for k < len(s) && isDigit(s[k]) {
			k++
		}
		if k > j+1 {
			j = k
		}
	}
	return j
}

// durationUnit reads a duration unit at i (ms, µs, us, ns, s, m, h, min): the end, or i.
func durationUnit(s string, i int) int {
	for _, u := range []string{"ms", "µs", "us", "ns", "min", "s", "m", "h"} {
		if strings.HasPrefix(s[i:], u) {
			end := i + len(u)
			if end < len(s) && isAlpha(s[end]) {
				continue
			}
			return end
		}
	}
	return i
}

// durationAt reads " (0.02s)" or " in 0.12s" at i (the space included): the end, or i.
func durationAt(s string, i int) int {
	j := i
	if j < len(s) && s[j] == ' ' {
		j++
	}
	switch {
	case strings.HasPrefix(s[j:], "("):
		k := number(s, j+1)
		if k == j+1 {
			return i
		}
		if k < len(s) && s[k] == ' ' {
			k++
		}
		u := durationUnit(s, k)
		if u == k || u >= len(s) || s[u] != ')' {
			return i
		}
		return u + 1
	case j > i && strings.HasPrefix(s[j:], "in "):
		k := number(s, j+3)
		if k == j+3 {
			return i
		}
		u := durationUnit(s, k)
		if u == k || (u < len(s) && isAlnum(s[u])) {
			return i
		}
		return u
	}
	return i
}

// digitsAt reports n digits at i.
func digitsAt(s string, i, n int) bool {
	if i+n > len(s) {
		return false
	}
	for k := i; k < i+n; k++ {
		if !isDigit(s[k]) {
			return false
		}
	}
	return true
}

// timestampAt reads an ISO timestamp (YYYY-MM-DD[T ]HH:MM:SS, fraction and zone optional) at i,
// with one space after it: the end, or i.
func timestampAt(s string, i int) int {
	if i > 0 && isAlnum(s[i-1]) {
		return i
	}
	if !digitsAt(s, i, 4) || i+19 > len(s) || s[i+4] != '-' || !digitsAt(s, i+5, 2) || s[i+7] != '-' || !digitsAt(s, i+8, 2) ||
		(s[i+10] != 'T' && s[i+10] != ' ') || !digitsAt(s, i+11, 2) || s[i+13] != ':' || !digitsAt(s, i+14, 2) || s[i+16] != ':' || !digitsAt(s, i+17, 2) {
		return i
	}
	j := i + 19
	if j < len(s) && (s[j] == '.' || s[j] == ',') {
		k := j + 1
		for k < len(s) && isDigit(s[k]) {
			k++
		}
		if k > j+1 {
			j = k
		}
	}
	switch {
	case j < len(s) && s[j] == 'Z':
		j++
	case j < len(s) && (s[j] == '+' || s[j] == '-') && digitsAt(s, j+1, 2):
		k := j + 3
		if k < len(s) && s[k] == ':' {
			k++
		}
		if digitsAt(s, k, 2) {
			j = k + 2
		}
	}
	if j < len(s) && s[j] == ' ' {
		j++
	}
	return j
}

// percentCounterAt reads a "[ 52%]" counter at i (a space before it included): the end, or i.
func percentCounterAt(s string, i int) int {
	j := i
	if j < len(s) && s[j] == ' ' {
		j++
	}
	if j >= len(s) || s[j] != '[' {
		return i
	}
	k := j + 1
	for k < len(s) && s[k] == ' ' {
		k++
	}
	d := k
	for k < len(s) && isDigit(s[k]) {
		k++
	}
	if k == d || k+1 >= len(s) || s[k] != '%' || s[k+1] != ']' {
		return i
	}
	return k + 2
}

// lineShape is a line with its digit runs masked, the key repeated lines collapse by.
func lineShape(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		if isDigit(line[i]) {
			if i == 0 || !isDigit(line[i-1]) {
				b.WriteByte('#')
			}
			continue
		}
		b.WriteByte(line[i])
	}
	return b.String()
}

// --- classes

// searchTools are the tools whose result is a search's (keys as toolKey writes them).
var searchTools = []string{"grep", "glob", "search", "ripgrep", "findfiles", "grepsearch", "searchfiles", "filesearch", "codebasesearch", "find", "ls", "listfiles", "listdir", "listdirectory"}

// commandSegments are a shell command's simple commands, each as its words: split at &&, ||,
// ;, |, & and line breaks, with quotes removed (a lenient reading: the class of a call only
// picks extractors, never a record's facts).
func commandSegments(command string) [][]string {
	out := [][]string{}
	words := []string{}
	var b strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			words = append(words, b.String())
		}
		b.Reset()
		inWord = false
	}
	end := func() {
		flush()
		if len(words) > 0 {
			out = append(out, words)
		}
		words = []string{}
	}
	var quote byte
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				b.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote, inWord = c, true
		case c == ' ' || c == '\t':
			flush()
		case c == '&' || c == '|' || c == ';' || c == '\n' || c == '(' || c == ')':
			end()
		default:
			b.WriteByte(c)
			inWord = true
		}
	}
	end()
	return out
}

// wrappers run the command after them: sudo, env, time...
var wrappers = []string{"sudo", "env", "time", "nice", "nohup", "command", "exec", "npx", "bunx", "pnpx", "xvfb-run"}

// programOf is a segment's program (its base name) and the word after it, past environment
// assignments, wrappers, "uv run", "poetry run" and "python -m".
func programOf(words []string) (string, string) {
	i := 0
	for i < len(words) {
		w := words[i]
		switch {
		case strings.Contains(w, "=") && !strings.HasPrefix(w, "-"):
			i++
			continue
		case containsString(wrappers, w):
			i++
			continue
		case w == "timeout" && i+1 < len(words):
			i += 2
			continue
		case (w == "uv" || w == "poetry" || w == "pipenv" || w == "pdm" || w == "hatch" || w == "rye") && i+1 < len(words) && words[i+1] == "run":
			i += 2
			continue
		}
		break
	}
	if i >= len(words) {
		return "", ""
	}
	prog := words[i]
	if k := strings.LastIndexByte(prog, '/'); k >= 0 {
		prog = prog[k+1:]
	}
	next := ""
	if i+1 < len(words) {
		next = words[i+1]
	}
	if (strings.HasPrefix(prog, "python") || prog == "py") && next == "-m" && i+2 < len(words) {
		prog, next = words[i+2], ""
		if i+3 < len(words) {
			next = words[i+3]
		}
	}
	return prog, next
}

// hasAlnum reports an ASCII letter or digit in s.
func hasAlnum(s string) bool {
	for i := 0; i < len(s); i++ {
		if isAlnum(s[i]) {
			return true
		}
	}
	return false
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// segmentClass is the class of one simple command.
func segmentClass(prog, next string) int {
	switch prog {
	case "pytest", "py.test", "tox", "nox", "jest", "vitest", "mocha", "ava", "phpunit", "rspec", "ctest", "unittest", "nextest":
		return classTest
	case "go":
		switch next {
		case "test":
			return classTest
		case "build", "vet", "install", "run", "generate":
			return classBuild
		}
	case "cargo":
		switch next {
		case "test", "nextest":
			return classTest
		case "build", "check", "clippy", "run":
			return classBuild
		}
	case "npm", "pnpm", "yarn", "bun", "deno":
		// A package manager runs a test, installs, or runs a script (a build, a type check, a lint).
		switch next {
		case "test", "t":
			return classTest
		case "", "install", "i", "add", "ci", "remove", "uninstall", "update", "upgrade", "view", "info", "ls", "list", "outdated", "audit", "init", "publish", "login", "whoami", "config":
			return classNone
		}
		return classBuild
	case "mvn", "mvnw", "gradle", "gradlew", "dotnet", "sbt", "bazel", "make", "gmake":
		if strings.Contains(next, "test") || next == "check" || next == "verify" {
			return classTest
		}
		return classBuild
	case "tsc", "gcc", "g++", "clang", "clang++", "cc", "c++", "mypy", "pyright", "eslint", "ruff", "flake8", "pylint",
		"rustc", "javac", "golangci-lint", "cmake", "ninja", "swiftc", "biome", "staticcheck", "shellcheck", "vue-tsc", "tsx", "node":
		return classBuild
	case "grep", "egrep", "fgrep", "rg", "ag", "ack", "find", "fd", "ls", "tree", "locate":
		return classSearch
	case "git":
		if next == "grep" || next == "ls-files" {
			return classSearch
		}
		return classGit
	}
	return classNone
}

// outputClass is a call's class: a search tool's, else the first test, build, search or git
// command among its simple commands, in that order of precedence.
func outputClass(call toolCall) int {
	if containsString(searchTools, toolKey(call.name)) {
		return classSearch
	}
	command, ok := shellCommand(call.args)
	if !ok {
		return classNone
	}
	best := classNone
	for _, words := range commandSegments(command) {
		c := segmentClass(programOf(words))
		if c != classNone && (best == classNone || c < best) {
			best = c
		}
	}
	return best
}

// --- extractors

// indented reports a line opening with a tab or at least two spaces.
func indented(line string) bool {
	return strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "  ")
}

// testFacts reads test runners' failures (go test, pytest, jest and vitest, cargo test, JUnit
// through maven and gradle): the failing test ids as test values, their lines, the first
// assertion lines after them and the tally as evidence.
func testFacts(f *facts, lines []string) {
	assertions := 0
	for i, line := range lines {
		t := trimBlank(line)
		switch {
		case strings.HasPrefix(t, "--- FAIL: "):
			// go test: "--- FAIL: TestName (0.01s)", then the test's indented output.
			name := firstWord(t[len("--- FAIL: "):])
			f.failed = true
			f.value(ValueTest, name)
			f.line(t)
			for k := i + 1; k < len(lines) && k <= i+3 && indented(lines[k]) && !strings.HasPrefix(trimBlank(lines[k]), "--- "); k++ {
				f.line(lines[k])
			}
		case (strings.HasPrefix(t, "FAILED ") || strings.HasPrefix(t, "ERROR ")) && strings.Contains(t, "::"):
			// pytest's short summary: "FAILED tests/test_x.py::test_y - AssertionError: ...".
			id := t[strings.IndexByte(t, ' ')+1:]
			if k := strings.Index(id, " - "); k >= 0 {
				id = id[:k]
			}
			f.failed = true
			f.value(ValueTest, trimBlank(id))
			f.line(t)
		case strings.HasPrefix(line, "E   ") && assertions < 3:
			// pytest's assertion lines.
			assertions++
			f.line(t)
		case strings.HasPrefix(t, "=") && strings.HasSuffix(t, "=") && (strings.Contains(t, " failed") || strings.Contains(t, " error")):
			// pytest's tally: "==== 1 failed, 2 passed in 0.12s ====".
			f.failed = true
			f.line(t)
		case strings.HasPrefix(t, "FAIL ") || strings.HasPrefix(t, "FAIL\t") || t == "FAIL":
			// go test's package line ("FAIL\tpkg"), jest's and vitest's file line ("FAIL src/x.test.ts").
			f.failed = true
			f.line(t)
		case strings.HasPrefix(t, "● ") && !strings.HasPrefix(t, "● Console"):
			// jest: "● Suite › test name".
			f.failed = true
			f.value(ValueTest, trimBlank(t[len("● "):]))
			f.line(t)
		case strings.HasPrefix(t, "Tests:") && strings.Contains(t, "failed"):
			f.failed = true
			f.line(t)
		case strings.HasPrefix(t, "test ") && strings.HasSuffix(t, " ... FAILED"):
			// cargo test: "test module::name ... FAILED".
			f.failed = true
			f.value(ValueTest, trimBlank(t[len("test "):len(t)-len(" ... FAILED")]))
			f.line(t)
		case strings.HasPrefix(t, "test result: FAILED"):
			f.failed = true
			f.line(t)
		case strings.HasPrefix(t, "Tests run:") && strings.Contains(t, "Failures:") && (failingCount(t, "Failures: ") || failingCount(t, "Errors: ")):
			// JUnit through maven (surefire) and ant.
			f.failed = true
			f.line(t)
		case strings.HasSuffix(t, " FAILED") && strings.Contains(t, " > "):
			// gradle: "com.example.FooTest > bar() FAILED".
			f.failed = true
			f.value(ValueTest, trimBlank(t[:len(t)-len(" FAILED")]))
			f.line(t)
		case strings.HasPrefix(t, "BUILD FAILED") || strings.HasPrefix(t, "BUILD FAILURE") || strings.HasPrefix(t, "[ERROR] BUILD FAILURE"):
			f.failed = true
			f.line(t)
		}
	}
}

// failingCount reports a nonzero count after label ("Failures: 2").
func failingCount(t, label string) bool {
	k := strings.Index(t, label)
	if k < 0 {
		return false
	}
	j := k + len(label)
	start := j
	for j < len(t) && isDigit(t[j]) {
		j++
	}
	return j > start && t[start:j] != "0"
}

// firstWord is s up to its first blank.
func firstWord(s string) string {
	for i := 0; i < len(s); i++ {
		if isBlank(s[i]) {
			return s[:i]
		}
	}
	return s
}

// diagnosticFacts reads compiler and linter diagnostics (gcc, clang, go build and vet, tsc,
// rustc, mypy, eslint): an error line ("file:line:col: error: message", tsc's
// "file(line,col): error TS1234: message", rustc's "error[E0425]: message" and the first
// "--> file:line:col" after it, eslint's "  1:2  error  message  rule") is evidence, and a
// failure. Only tsc's and eslint's shapes are read in any command's output (build says the
// command is a build or a test): a grep result shares the others' shape.
func diagnosticFacts(f *facts, lines []string, build bool) {
	arrow := false
	for _, line := range lines {
		t := trimBlank(line)
		switch {
		case build && (strings.HasPrefix(t, "error[") || strings.HasPrefix(t, "error:")):
			f.failed = true
			f.line(t)
			arrow = true
		case build && arrow && strings.HasPrefix(t, "--> "):
			f.line(t)
			arrow = false
		case strings.Contains(t, ": error TS") || (build && diagnosticSeverity(t) == "error"):
			f.failed = true
			f.line(t)
		case eslintError(t):
			f.failed = true
			f.line(t)
		}
	}
}

// diagnosticSeverity is the severity of a "file:line[:col]: severity: message" line ("error"
// for error and fatal error), "" for a line of another shape or with none.
func diagnosticSeverity(t string) string {
	k := strings.IndexByte(t, ':')
	if k <= 0 || !strings.Contains(t[:k], ".") || strings.IndexByte(t[:k], ' ') >= 0 {
		return ""
	}
	j := k + 1
	start := j
	for j < len(t) && isDigit(t[j]) {
		j++
	}
	if j == start || j >= len(t) || t[j] != ':' {
		return ""
	}
	j++
	if col := j; j < len(t) && isDigit(t[j]) {
		for j < len(t) && isDigit(t[j]) {
			j++
		}
		if j >= len(t) || t[j] != ':' {
			j = col
		} else {
			j++
		}
	}
	rest := lowerASCII(trimBlank(t[j:]))
	if strings.HasPrefix(rest, "error") || strings.HasPrefix(rest, "fatal error") {
		return "error"
	}
	return ""
}

// eslintError reports eslint's stylish line: "12:5  error  message  rule".
func eslintError(t string) bool {
	j := 0
	for j < len(t) && isDigit(t[j]) {
		j++
	}
	if j == 0 || j >= len(t) || t[j] != ':' {
		return false
	}
	k := j + 1
	for k < len(t) && isDigit(t[k]) {
		k++
	}
	if k == j+1 {
		return false
	}
	return strings.HasPrefix(t[k:], "  error  ")
}

// searchFacts reads the identifier a search looked for: a search tool's pattern argument, or
// the first operand of a grep or rg command, when it is one identifier of three or more
// characters.
func searchFacts(f *facts, call toolCall) {
	pattern, _ := call.args["pattern"].(string)
	if pattern == "" {
		if command, ok := shellCommand(call.args); ok {
			for _, words := range commandSegments(command) {
				prog, _ := programOf(words)
				if prog != "grep" && prog != "rg" && prog != "egrep" && prog != "ag" && prog != "ack" {
					continue
				}
				for _, w := range words[1:] {
					if !strings.HasPrefix(w, "-") && w != prog {
						pattern = w
						break
					}
				}
				break
			}
		}
	}
	if len(pattern) >= 3 && !isDigit(pattern[0]) {
		for i := 0; i < len(pattern); i++ {
			if !isIdent(pattern[i]) {
				return
			}
		}
		f.value(ValueSymbol, pattern)
	}
}

// gitStatusLabels open git status's long-format lines.
var gitStatusLabels = []string{"modified:", "new file:", "deleted:", "renamed:", "both modified:", "both added:", "typechange:", "copied:"}

// gitFacts reads git's output: branch names, the paths status lists (also without a slash),
// and conflicts and errors as evidence.
func gitFacts(f *facts, lines []string) {
	for _, line := range lines {
		t := trimBlank(line)
		switch {
		case strings.HasPrefix(t, "On branch "):
			f.value(ValueBranch, firstWord(t[len("On branch "):]))
		case strings.HasPrefix(t, "Switched to branch '") || strings.HasPrefix(t, "Switched to a new branch '") || strings.HasPrefix(t, "Your branch is "):
			f.value(ValueBranch, quotedName(t))
		case strings.HasPrefix(t, "Saved working directory and index state WIP on "):
			rest := t[len("Saved working directory and index state WIP on "):]
			if k := strings.IndexByte(rest, ':'); k > 0 {
				f.value(ValueBranch, rest[:k])
			}
		case strings.HasPrefix(t, "* ") && len(firstWord(t[2:])) > 0 && !strings.HasPrefix(t, "* ("):
			f.value(ValueBranch, firstWord(t[2:]))
		case strings.HasPrefix(t, "CONFLICT ") || strings.HasPrefix(t, "error: ") || strings.HasPrefix(t, "fatal: ") || strings.HasPrefix(t, "Automatic merge failed") || strings.HasPrefix(t, "! [rejected]"):
			f.failed = true
			f.line(t)
		}
		for _, label := range gitStatusLabels {
			if strings.HasPrefix(t, label) {
				rest := trimBlank(t[len(label):])
				if k := strings.Index(rest, " -> "); k >= 0 {
					gitPath(f, rest[:k])
					rest = rest[k+len(" -> "):]
				}
				gitPath(f, rest)
			}
		}
		// Porcelain status: two status characters, a space, the path.
		if len(line) > 3 && line[2] == ' ' && strings.IndexByte(" MADRCU?!", line[0]) >= 0 && strings.IndexByte(" MADRCU?!", line[1]) >= 0 && (line[0] != ' ' || line[1] != ' ') {
			rest := line[3:]
			if k := strings.Index(rest, " -> "); k >= 0 {
				rest = rest[k+len(" -> "):]
			}
			gitPath(f, rest)
		}
	}
}

// gitPath adds a path git printed: one word with an extension, a slash not required.
func gitPath(f *facts, p string) {
	p = trimBlank(p)
	if p == "" || strings.IndexByte(p, ' ') >= 0 {
		return
	}
	if strings.IndexByte(p, '/') >= 0 {
		if v, ok := pathValue(p); ok {
			f.value(ValuePath, v)
		}
		return
	}
	if hasExtension(p) {
		f.value(ValuePath, p)
	}
}

// quotedName is the first '...'-quoted name in t, "" when none.
func quotedName(t string) string {
	k := strings.IndexByte(t, '\'')
	if k < 0 {
		return ""
	}
	end := strings.IndexByte(t[k+1:], '\'')
	if end < 0 {
		return ""
	}
	return t[k+1 : k+1+end]
}

// stackFacts reads stack traces (Python, Go, JS and Node, Java, Rust): the error line and the
// outer and inner frames as evidence, and a failure; frame paths and error types are values
// the generic scan gives.
func stackFacts(f *facts, lines []string) {
	for i := 0; i < len(lines); i++ {
		t := trimBlank(lines[i])
		switch {
		case t == "Traceback (most recent call last):":
			// Python: frames "  File "p", line N, in fn", then the exception line.
			f.failed = true
			frames := []string{}
			k := i + 1
			for ; k < len(lines); k++ {
				ft := trimBlank(lines[k])
				if strings.HasPrefix(ft, "File \"") {
					frames = append(frames, ft)
					continue
				}
				if indented(lines[k]) || ft == "" {
					continue
				}
				break
			}
			if k < len(lines) {
				f.line(lines[k])
			}
			pythonFrames(f, frames)
			i = k
		case strings.HasPrefix(t, "panic: ") || strings.HasPrefix(t, "fatal error: "):
			// Go: "panic: msg", then "goroutine N [running]:" and "\t/path/x.go:12 +0x1d" frames.
			f.failed = true
			f.line(t)
			shown := 0
			for k := i + 1; k < len(lines) && shown < 2; k++ {
				ft := trimBlank(lines[k])
				if strings.HasPrefix(lines[k], "\t") && strings.Contains(ft, ".go:") {
					if p := strings.Index(ft, " +0x"); p >= 0 {
						ft = ft[:p]
					}
					f.line(ft)
					shown++
				}
			}
		case strings.HasPrefix(t, "thread '") && strings.Contains(t, "' panicked at "):
			// Rust: "thread 'main' panicked at src/main.rs:5:9:", the message on the next line.
			f.failed = true
			f.line(t)
			if i+1 < len(lines) && !strings.HasPrefix(trimBlank(lines[i+1]), "note:") {
				f.line(lines[i+1])
			}
		case strings.HasPrefix(t, "at ") && indented(lines[i]) && (i == 0 || !strings.HasPrefix(trimBlank(lines[i-1]), "at ")):
			// JS, Node and Java: "    at fn (file:line:col)" or "\tat pkg.Class.method(File.java:12)"
			// frames, after the error line.
			if i > 0 {
				head := trimBlank(lines[i-1])
				if errorLine(head) {
					f.failed = true
					f.line(head)
					f.line(t)
				}
			}
		case strings.HasPrefix(t, "Caused by: "):
			f.failed = true
			f.line(t)
		}
	}
}

// pythonFrames adds a Python traceback's first frame, its last project frame (not under
// site-packages, dist-packages, /usr/lib or <frozen) and its last frame.
func pythonFrames(f *facts, frames []string) {
	if len(frames) == 0 {
		return
	}
	f.line(frames[0])
	project := -1
	for k, fr := range frames {
		if !strings.Contains(fr, "site-packages") && !strings.Contains(fr, "dist-packages") && !strings.Contains(fr, "/usr/lib") && !strings.Contains(fr, "<frozen") {
			project = k
		}
	}
	if project > 0 {
		f.line(frames[project])
	}
	f.line(frames[len(frames)-1])
}

// errorLine reports a line a stack trace's frames follow: it names an error or exception
// type, or opens "Error:" or "Exception in thread".
func errorLine(t string) bool {
	if strings.HasPrefix(t, "Error:") || strings.HasPrefix(t, "Exception in thread") || strings.HasPrefix(t, "Uncaught ") {
		return true
	}
	found := false
	scanErrorTypes(t, func(string) { found = true })
	return found
}

// failureStarts open a line that reports a failure, compared lower-cased.
var failureStarts = []string{"error:", "error[", "fatal:", "fatal error", "panic:", "npm err!", "exception:", "make: ***", "uncaught ", "unhandled ", "err!", "error ts"}

// failureContains are phrases that report a failure anywhere in a line, compared lower-cased.
var failureContains = []string{"command not found", "no such file or directory", "permission denied", "connection refused", "traceback (most recent call last)", "segmentation fault", "command failed with exit code", "tool execution failed"}

// markerFacts reads lines that report a failure, whatever the tool printed them.
func markerFacts(f *facts, lines []string) {
	for _, line := range lines {
		t := lowerASCII(trimBlank(line))
		if t == "" {
			continue
		}
		hit := false
		for _, p := range failureStarts {
			if strings.HasPrefix(t, p) {
				hit = true
				break
			}
		}
		if !hit {
			for _, p := range failureContains {
				if strings.Contains(t, p) {
					hit = true
					break
				}
			}
		}
		if hit {
			f.failed = true
			f.line(line)
		}
	}
}

// failureWords are the words a failed result's evidence lines are picked by, lower-cased.
var failureWords = []string{"error", "fail", "fatal", "panic", "exception", "denied", "cannot", "unable", "refused", "timeout", "timed out", "not found", "invalid", "missing"}

// fallbackEvidence picks a failed result's evidence when no grammar matched: its first line,
// the lines holding a failure word, and its last line, in order.
func fallbackEvidence(f *facts, lines []string) {
	first, last := -1, -1
	for i, line := range lines {
		if evidenceLine(line) != "" {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return
	}
	for i := first; i <= last; i++ {
		if i == first || i == last {
			f.line(lines[i])
			continue
		}
		t := lowerASCII(lines[i])
		for _, w := range failureWords {
			if strings.Contains(t, w) {
				f.line(lines[i])
				break
			}
		}
	}
}

// --- the generic value scan

// scanValues adds the values one line holds, kind by kind in scan order (URLs, paths,
// KEY=value pairs, addresses, SHAs, error types, issue numbers, versions), each kind in line
// order. git says the line is git's output, where a short hex string is a SHA.
func scanValues(f *facts, line string, git bool) {
	if line == "" {
		return
	}
	spans := urlSpans(line)
	for _, s := range spans {
		f.value(ValueURL, s.value)
	}
	scanPaths(line, spans, func(v string) { f.value(ValuePath, v) })
	scanKVs(line, func(v string) { f.value(ValueKV, v) })
	scanIPs(line, func(v string) { f.value(ValueIP, v) })
	scanSHAs(line, git || gitLine(line), func(v string) { f.value(ValueSHA, v) })
	scanErrorTypes(line, func(v string) { f.value(ValueError, v) })
	scanIssues(line, func(v string) { f.value(ValueIssue, v) })
	scanVersions(line, func(v string) { f.value(ValueVersion, v) })
}

// gitLines open a line of git output wherever it is printed.
var gitLines = []string{"commit ", "index ", "Merge: ", "HEAD is now at ", "Saved working directory", "Author: "}

func gitLine(line string) bool {
	t := trimBlank(line)
	for _, p := range gitLines {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// span is a value found at [start, end) of a line.
type span struct {
	start, end int
	value      string
}

// urlStops end a URL.
const urlStops = "\"'<>`"

// urlSpans are the line's http and https URLs: from the scheme to a blank or one of urlStops,
// without trailing punctuation or an unbalanced closing bracket, cut to maxURLChars characters.
func urlSpans(line string) []span {
	out := []span{}
	for i := 0; i < len(line); {
		k := strings.Index(line[i:], "http")
		if k < 0 {
			break
		}
		k += i
		i = k + 4
		rest := line[k:]
		var scheme int
		switch {
		case strings.HasPrefix(rest, "https://"):
			scheme = len("https://")
		case strings.HasPrefix(rest, "http://"):
			scheme = len("http://")
		default:
			continue
		}
		if k > 0 && isAlnum(line[k-1]) {
			continue
		}
		end := k + scheme
		for end < len(line) && !isBlank(line[end]) && strings.IndexByte(urlStops, line[end]) < 0 {
			end++
		}
		end = trimURLEnd(line, k+scheme, end)
		if end <= k+scheme {
			continue
		}
		out = append(out, span{start: k, end: end, value: clipRunes(line[k:end], maxURLChars)})
		i = end
	}
	return out
}

// trimURLEnd drops trailing punctuation (.,;:!?) and a closing ) ] } the URL does not open.
func trimURLEnd(line string, from, end int) int {
	for end > from {
		c := line[end-1]
		if strings.IndexByte(".,;:!?", c) >= 0 {
			end--
			continue
		}
		if open := strings.IndexByte(")]}", c); open >= 0 {
			if strings.Count(line[from:end], string("([{"[open])) < strings.Count(line[from:end], string(c)) {
				end--
				continue
			}
		}
		break
	}
	return end
}

// clipRunes is the first n characters of s.
func clipRunes(s string, n int) string {
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// isPathChar is a character a path holds: letters, digits, _ - . / \ @ ~ + % and any non-ASCII.
func isPathChar(c byte) bool {
	return isAlnum(c) || c == '_' || c == '-' || c == '.' || c == '/' || c == '\\' || c == '@' || c == '~' || c == '+' || c == '%' || c >= 0x80
}

// pathExcluded are the parts of a path that make it a dependency's or the system's.
var pathExcluded = []string{"node_modules/", "site-packages/", "dist-packages/", ".git/objects/"}

// pathExcludedPrefixes open system paths.
var pathExcludedPrefixes = []string{"/proc/", "/usr/lib/", "/usr/local/lib/", "/usr/share/", "/sys/"}

// scanPaths finds the line's file paths: runs of path characters (a drive letter's "X:" kept),
// outside URLs, that pathValue accepts.
func scanPaths(line string, urls []span, add func(string)) {
	n := len(line)
	for i := 0; i < n; {
		if !isPathChar(line[i]) || (i > 0 && isPathChar(line[i-1])) {
			i++
			continue
		}
		end := i
		for end < n && isPathChar(line[end]) {
			end++
		}
		// A drive letter: "D:" then a slash continues the path.
		if end == i+1 && isAlpha(line[i]) && end+1 < n && line[end] == ':' && (line[end+1] == '/' || line[end+1] == '\\') && (i == 0 || !isAlnum(line[i-1])) {
			end += 1
			for end < n && isPathChar(line[end]) {
				end++
			}
		}
		inURL := false
		for _, s := range urls {
			if i >= s.start && i < s.end {
				inURL = true
			}
		}
		if !inURL {
			if v, ok := pathValue(line[i:end]); ok {
				add(v)
			}
		}
		i = end
	}
}

// pathValue is a path token as the index keeps it: leading and trailing non-ASCII and trailing
// dots dropped, each run of backslashes one slash; ok when it holds a slash, its last part has
// an extension (hasExtension) and it is no dependency's or system path.
func pathValue(token string) (string, bool) {
	start, end := 0, len(token)
	for start < end && token[start] >= 0x80 {
		start++
	}
	for end > start && (token[end-1] >= 0x80 || token[end-1] == '.') {
		end--
	}
	token = token[start:end]
	for i := 0; i+1 < len(token); i++ {
		// A C-style escape (git's quoted "\345\216..."): the path's real spelling is not printed.
		if token[i] == '\\' && isDigit(token[i+1]) {
			return "", false
		}
	}
	if strings.IndexByte(token, '\\') >= 0 {
		var b strings.Builder
		for i := 0; i < len(token); i++ {
			if token[i] == '\\' {
				if i == 0 || token[i-1] != '\\' {
					b.WriteByte('/')
				}
				continue
			}
			b.WriteByte(token[i])
		}
		token = b.String()
	}
	if strings.IndexByte(token, '/') < 0 || utf8.RuneCountInString(token) > maxValueChars {
		return "", false
	}
	base := token[strings.LastIndexByte(token, '/')+1:]
	if !hasExtension(base) {
		return "", false
	}
	for _, x := range pathExcluded {
		if strings.Contains(token, x) {
			return "", false
		}
	}
	for _, x := range pathExcludedPrefixes {
		if strings.HasPrefix(token, x) {
			return "", false
		}
	}
	return token, true
}

// hasExtension reports a file name ending in a dot and one to eight ASCII letters and digits,
// at least one a letter.
func hasExtension(base string) bool {
	dot := strings.LastIndexByte(base, '.')
	if dot < 0 {
		return false
	}
	ext := base[dot+1:]
	if len(ext) == 0 || len(ext) > 8 {
		return false
	}
	letter := false
	for i := 0; i < len(ext); i++ {
		if !isAlnum(ext[i]) {
			return false
		}
		if isAlpha(ext[i]) {
			letter = true
		}
	}
	return letter
}

// secretKeyParts are the parts of a KEY=value name that make its value a secret, upper-cased.
var secretKeyParts = []string{"SECRET", "TOKEN", "PASSW", "API_KEY", "APIKEY", "PRIVATE", "CREDENTIAL", "AUTH"}

// scanKVs finds the line's KEY=value pairs: an upper-case name of two or more characters
// ([A-Z][A-Z0-9_]+, not inside a word) right before =, and the non-blank run after it. A name
// that looks secret (secretKeyParts) or a value of 40 or more key-like characters is skipped.
func scanKVs(line string, add func(string)) {
	n := len(line)
	for i := 0; i < n; i++ {
		if !isUpper(line[i]) || (i > 0 && (isIdent(line[i-1]) || line[i-1] == '$' || line[i-1] == '-')) {
			continue
		}
		j := i
		for j < n && (isUpper(line[j]) || isDigit(line[j]) || line[j] == '_') {
			j++
		}
		if j-i < 2 || j >= n || line[j] != '=' {
			i = j - 1
			continue
		}
		v := j + 1
		end := v
		for end < n && !isBlank(line[end]) {
			end++
		}
		if end == v || line[v] == '=' {
			i = end - 1
			continue
		}
		key, value := line[i:j], line[v:end]
		if !secretKey(key) && !keyLike(value) {
			add(line[i:end])
		}
		i = end - 1
	}
}

func secretKey(key string) bool {
	for _, p := range secretKeyParts {
		if strings.Contains(key, p) {
			return true
		}
	}
	return false
}

// keyLike reports a value of 40 or more characters of [A-Za-z0-9+/_=-]: a key, a token or a
// digest, never indexed.
func keyLike(value string) bool {
	if len(value) < 40 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !isAlnum(c) && strings.IndexByte("+/_=-", c) < 0 {
			return false
		}
	}
	return true
}

// scanIPs finds the line's IPv4 addresses, each octet at most 255, with an optional :port; a
// dotted run of more than four numbers (a version) is none.
func scanIPs(line string, add func(string)) {
	n := len(line)
	for i := 0; i < n; i++ {
		if !isDigit(line[i]) || (i > 0 && (isAlnum(line[i-1]) || line[i-1] == '.')) {
			continue
		}
		j := i
		ok := true
		for part := 0; part < 4; part++ {
			start := j
			for j < n && j-start < 3 && isDigit(line[j]) {
				j++
			}
			if j == start || (j < n && isDigit(line[j])) {
				ok = false
				break
			}
			if v, _ := strconv.Atoi(line[start:j]); v > 255 {
				ok = false
				break
			}
			if part < 3 {
				if j >= n || line[j] != '.' {
					ok = false
					break
				}
				j++
			}
		}
		if !ok {
			continue
		}
		if j < n && (isAlnum(line[j]) || (line[j] == '.' && j+1 < n && isDigit(line[j+1]))) {
			continue
		}
		end := j
		if j+1 < n && line[j] == ':' && isDigit(line[j+1]) {
			k := j + 1
			for k < n && isDigit(line[k]) {
				k++
			}
			if port, _ := strconv.Atoi(line[j+1 : k]); k-(j+1) <= 5 && port <= 65535 && (k >= n || !isAlnum(line[k])) {
				end = k
			}
		}
		add(line[i:end])
		i = end - 1
	}
}

// scanSHAs finds the line's commit hashes: runs of 7 to 40 lower-case hex characters, not
// inside a word, with a digit and a letter; a run shorter than 40 only in git's output.
func scanSHAs(line string, git bool, add func(string)) {
	n := len(line)
	for i := 0; i < n; i++ {
		if !isHex(line[i]) || (i > 0 && (isIdent(line[i-1]))) {
			continue
		}
		j := i
		digit, letter := false, false
		for j < n && isHex(line[j]) {
			if isDigit(line[j]) {
				digit = true
			} else {
				letter = true
			}
			j++
		}
		if j < n && isIdent(line[j]) {
			for j < n && isIdent(line[j]) {
				j++
			}
			i = j - 1
			continue
		}
		length := j - i
		if digit && letter && (length == 40 || (git && length >= 7 && length <= 40)) {
			add(line[i:j])
		}
		i = j - 1
	}
}

// errorSuffixes end an error type's name.
var errorSuffixes = []string{"Error", "Exception", "Denied", "Timeout", "Overflow"}

// scanErrorTypes finds the line's error type names: an identifier opening with an upper-case
// letter and ending in one of errorSuffixes (longer than the suffix), or Go's ErrName.
func scanErrorTypes(line string, add func(string)) {
	n := len(line)
	for i := 0; i < n; i++ {
		if !isIdent(line[i]) || (i > 0 && isIdent(line[i-1])) {
			continue
		}
		j := i
		for j < n && isIdent(line[j]) {
			j++
		}
		word := line[i:j]
		i = j - 1
		if !isUpper(word[0]) {
			continue
		}
		if len(word) > 3 && strings.HasPrefix(word, "Err") && isUpper(word[3]) {
			add(word)
			continue
		}
		for _, s := range errorSuffixes {
			if len(word) > len(s) && strings.HasSuffix(word, s) {
				add(word)
				break
			}
		}
	}
}

// scanIssues finds the line's issue and pull request numbers: # and one to six digits, not
// inside a word or after & (an HTML entity).
func scanIssues(line string, add func(string)) {
	n := len(line)
	for i := 0; i < n; i++ {
		if line[i] != '#' || (i > 0 && (isAlnum(line[i-1]) || line[i-1] == '&' || line[i-1] == '#')) {
			continue
		}
		j := i + 1
		for j < n && isDigit(line[j]) {
			j++
		}
		if j == i+1 || j-(i+1) > 6 || (j < n && isIdent(line[j])) {
			continue
		}
		add(line[i:j])
		i = j - 1
	}
}

// scanVersions finds the line's versions: name@1.2.3 (an npm-style name, @scope/ allowed) and
// v1.2 or v1.2.3, with an optional -suffix.
func scanVersions(line string, add func(string)) {
	n := len(line)
	for i := 0; i < n; i++ {
		switch {
		case line[i] == '@' && i > 0 && i+1 < n && isDigit(line[i+1]):
			end := versionEnd(line, i+1)
			if end < 0 {
				continue
			}
			start := i
			for start > 0 && (isAlnum(line[start-1]) || strings.IndexByte("_./-", line[start-1]) >= 0) {
				start--
			}
			if start > 0 && line[start-1] == '@' && (start == 1 || !isAlnum(line[start-2])) {
				start--
			}
			if hasAlnum(line[start:i]) {
				add(line[start:end])
			}
			i = end - 1
		case line[i] == 'v' && (i == 0 || !isAlnum(line[i-1])) && i+1 < n && isDigit(line[i+1]):
			end := versionEnd(line, i+1)
			if end < 0 {
				continue
			}
			add(line[i:end])
			i = end - 1
		}
	}
}

// versionEnd is the end of a version at i (digits, then one or more .digits, then an optional
// -suffix of letters, digits and dots), -1 when there is none or it runs into a word.
func versionEnd(s string, i int) int {
	j := i
	for j < len(s) && isDigit(s[j]) {
		j++
	}
	if j == i {
		return -1
	}
	parts := 0
	for j+1 < len(s) && s[j] == '.' && isDigit(s[j+1]) {
		j++
		for j < len(s) && isDigit(s[j]) {
			j++
		}
		parts++
	}
	if parts == 0 {
		return -1
	}
	if j+1 < len(s) && s[j] == '-' && isAlnum(s[j+1]) {
		j++
		for j < len(s) && (isAlnum(s[j]) || s[j] == '.') {
			j++
		}
		for j > i && s[j-1] == '.' {
			j--
		}
	}
	if j < len(s) && (isIdent(s[j])) {
		return -1
	}
	return j
}

// --- records

// valueContent is a value record's content: its kind and value, and how many results printed
// it when more than one.
func valueContent(kind, value string, count int) string {
	content := kind + " " + value
	if count > 1 {
		content += " (seen " + strconv.Itoa(count) + " times)"
	}
	return content
}

// ValueParts are a value record's kind, value and count: the kind and value from its target
// ("kind:value"), the count from its content's "(seen N times)", else 1. ok is false for a
// record that is not a value record.
func ValueParts(o ledger.Observation) (kind, value string, count int, ok bool) {
	if o.Kind != ledger.KindValue {
		return "", "", 0, false
	}
	kind, value, found := strings.Cut(o.Target, ":")
	if !found || kind == "" || value == "" {
		return "", "", 0, false
	}
	count = 1
	if strings.HasSuffix(o.Content, " times)") {
		if k := strings.LastIndex(o.Content, " (seen "); k >= 0 && k+len(" (seen ") <= len(o.Content)-len(" times)") {
			if n, err := strconv.Atoi(o.Content[k+len(" (seen ") : len(o.Content)-len(" times)")]); err == nil && n > 1 {
				count = n
			}
		}
	}
	return kind, value, count, true
}

// valueState is one value's sighting in an extraction: the ledger's record of it, continued.
type valueState struct {
	kind, value string
	count       int
	// firstID is the entry the value was first seen in; newest the branch index of the
	// latest result printing it in this extraction, -1 when none did.
	firstID string
	newest  int
	// prior are the active records of the value the new one supersedes.
	prior []string
}

// outputRecords reads one call's result into value and evidence records, anchored to the
// result's entry, so they fall into that entry's cut-point segment batch.
func (x *extraction) outputRecords(call toolCall, result toolResult, key, display string) {
	if !result.present || result.index < 0 || result.index >= len(x.in.Entries) {
		return
	}
	f := outputFactsOf(call, result.text, result.failed)
	added := 0
	for _, v := range f.values {
		key := v.kind + ":" + v.value
		st := x.values[key]
		if st == nil {
			if added >= maxNewValues {
				continue
			}
			added++
			st = &valueState{kind: v.kind, value: v.value, firstID: x.in.Entries[result.index].ID, newest: -1}
			x.values[key] = st
		}
		if st.newest == result.index {
			continue
		}
		if st.newest < 0 {
			x.valueOrder = append(x.valueOrder, key)
		}
		st.count++
		st.newest = result.index
	}
	if len(f.evidence) > 0 {
		content := call.name + " " + strconv.Quote(clip(display, 80)) + " output: " + strings.Join(f.evidence, EvidenceSeparator)
		x.add(ledger.Observation{Content: content, Kind: ledger.KindEvidence, Tool: call.name, Target: key}, result.index)
	}
}

// loadValues reads the ledger's active, unsuperseded value records into the extraction's
// value states.
func (x *extraction) loadValues(active []ledger.Observation) {
	for _, o := range active {
		if x.superseded[o.ID] {
			continue
		}
		kind, value, count, ok := ValueParts(o)
		if !ok || len(o.SourceEntryIDs) == 0 {
			continue
		}
		key := kind + ":" + value
		st := x.values[key]
		if st == nil {
			st = &valueState{kind: kind, value: value, firstID: o.SourceEntryIDs[0], newest: -1}
			x.values[key] = st
		}
		st.count = max(st.count, count)
		st.prior = append(st.prior, o.ID)
	}
}

// emitValues appends one record per value seen in the extraction, at its latest sighting,
// citing its first sighting and that one, with its count; it supersedes the value's earlier
// record.
func (x *extraction) emitValues() {
	for _, key := range x.valueOrder {
		st := x.values[key]
		obs := ledger.Observation{Content: valueContent(st.kind, st.value, st.count), Kind: ledger.KindValue, Target: key}
		if st.firstID != "" {
			obs.SourceEntryIDs = []string{st.firstID}
		}
		obs.Supersedes = append(obs.Supersedes, st.prior...)
		x.add(obs, st.newest)
	}
}
