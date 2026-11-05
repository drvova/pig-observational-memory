package extract

import (
	"regexp"
	"strings"
)

// shellWord is one word of a shell command, with whether it is a literal: no unquoted glob,
// brace, tilde or variable that the shell would expand.
type shellWord struct {
	text    string
	literal bool
}

// unsafeShell is shell syntax that makes a command's effect on a path depend on more than its
// words: sequencing other than &&, pipes, substitutions, redirections, background jobs and
// line breaks.
var unsafeShell = regexp.MustCompile("[;|<>`\n]|\\$\\(|(^|[^&])&([^&]|$)")

// simpleCommands splits command into its simple commands joined by &&, each as its words, when
// the whole command is unambiguous: no other sequencing or substitution (unsafeShell), no
// change of directory (cd, pushd, popd, or git -C), and quotes that close. A command that
// succeeded ran every one of them successfully. ok is false otherwise.
func simpleCommands(command string) (commands [][]shellWord, ok bool) {
	command = strings.TrimSpace(command)
	if command == "" || unsafeShell.MatchString(command) {
		return nil, false
	}
	for _, part := range strings.Split(command, "&&") {
		words, ok := shellWords(part)
		if !ok || len(words) == 0 {
			return nil, false
		}
		switch words[0].text {
		case "cd", "pushd", "popd":
			return nil, false
		}
		if strings.Contains(words[0].text, "=") {
			// An environment assignment prefix: what follows runs with a changed environment.
			return nil, false
		}
		if words[0].text == "git" {
			for _, w := range words[1:] {
				if w.text == "-C" || strings.HasPrefix(w.text, "--git-dir") || strings.HasPrefix(w.text, "--work-tree") {
					return nil, false
				}
			}
		}
		commands = append(commands, words)
	}
	return commands, true
}

// shellWords splits one simple command into words, honoring single quotes, double quotes and
// backslash escapes. A double-quoted $ is not literal; ok is false when a quote does not close.
func shellWords(part string) (words []shellWord, ok bool) {
	var b strings.Builder
	inWord, literal := false, true
	flush := func() {
		if inWord {
			words = append(words, shellWord{text: b.String(), literal: literal})
		}
		b.Reset()
		inWord, literal = false, true
	}
	for i := 0; i < len(part); i++ {
		c := part[i]
		switch {
		case c == ' ' || c == '\t':
			flush()
		case c == '\'':
			end := strings.IndexByte(part[i+1:], '\'')
			if end < 0 {
				return nil, false
			}
			b.WriteString(part[i+1 : i+1+end])
			inWord, i = true, i+1+end
		case c == '"':
			end := strings.IndexByte(part[i+1:], '"')
			if end < 0 {
				return nil, false
			}
			quoted := part[i+1 : i+1+end]
			if strings.ContainsAny(quoted, "$\\") {
				literal = false
			}
			b.WriteString(quoted)
			inWord, i = true, i+1+end
		case c == '\\':
			if i+1 < len(part) {
				b.WriteByte(part[i+1])
				i++
			}
			inWord = true
		default:
			if strings.IndexByte("*?[]{}~$", c) >= 0 {
				literal = false
			}
			b.WriteByte(c)
			inWord = true
		}
	}
	flush()
	return words, true
}

// pathEvent is a change a successful shell command made to a path.
type pathEvent struct {
	path, state, other string
}

// operands are a command's words after its leading ones, without options; an option ends at --
// when endOptions, after which every word is an operand.
func operands(words []shellWord) (out []shellWord, options []string) {
	endOptions := false
	for _, w := range words {
		if !endOptions && w.text == "--" {
			endOptions = true
			continue
		}
		if !endOptions && strings.HasPrefix(w.text, "-") && w.text != "-" {
			options = append(options, w.text)
			continue
		}
		out = append(out, w)
	}
	return out, options
}

func hasOption(options []string, names ...string) bool {
	for _, option := range options {
		for _, name := range names {
			if option == name || strings.HasPrefix(option, name+"=") {
				return true
			}
		}
	}
	return false
}

// shellPathEvents are the path changes a successful command certainly made (docs/memory-v2.md,
// "Work facts"): an exact literal path in rm, git rm (not --cached) or git mv (one source, one
// destination) is deleted or moved, and git checkout -- path or git restore path (touching the
// worktree, from no other source) is reverted. A command that is not unambiguous
// (simpleCommands) yields none.
func shellPathEvents(command string) []pathEvent {
	commands, ok := simpleCommands(command)
	if !ok {
		return nil
	}
	events := []pathEvent{}
	literalPaths := func(words []shellWord, state string) {
		for _, w := range words {
			if w.literal && w.text != "" {
				events = append(events, pathEvent{path: w.text, state: state})
			}
		}
	}
	for _, words := range commands {
		switch {
		case words[0].text == "rm":
			// An interactive rm asks first: without a terminal it reads no answer, keeps the file
			// and still exits 0.
			paths, options := operands(words[1:])
			if !interactiveRm(options) {
				literalPaths(paths, stateDeleted)
			}
		case words[0].text == "git" && len(words) > 1:
			sub := words[1].text
			rest := words[2:]
			switch sub {
			case "rm":
				paths, options := operands(rest)
				if !hasOption(options, "--cached", "-n", "--dry-run") {
					literalPaths(paths, stateDeleted)
				}
			case "mv":
				paths, options := operands(rest)
				if len(paths) == 2 && paths[0].literal && paths[1].literal && !hasOption(options, "-n", "--dry-run") {
					events = append(events, pathEvent{path: paths[0].text, state: stateMovedTo, other: paths[1].text}, pathEvent{path: paths[1].text, state: stateMovedFrom, other: paths[0].text})
				}
			case "checkout":
				dash := -1
				for i, w := range rest {
					if w.text == "--" {
						dash = i
						break
					}
				}
				// Only after "--" is a word certainly a path: before it, it may name a branch.
				// A patch checkout reverts only the hunks a terminal answer picks: none, or part.
				if dash >= 0 && dash == len(optionsPrefix(rest)) && !patchMode(optionsPrefix(rest)) {
					literalPaths(rest[dash+1:], stateReverted)
				}
			case "restore":
				paths, options := operands(rest)
				staged := hasOption(options, "--staged", "-S")
				worktree := hasOption(options, "--worktree", "-W")
				if (!staged || worktree) && !hasOption(options, "--source", "-s") && !patchModeOptions(options) {
					literalPaths(paths, stateReverted)
				}
			}
		}
	}
	return events
}

// interactiveRm reports rm options that ask before removing (-i, -I, --interactive), alone or
// in a bundle such as -ri.
func interactiveRm(options []string) bool {
	for _, option := range options {
		if option == "--interactive" || strings.HasPrefix(option, "--interactive=") && option != "--interactive=never" {
			return true
		}
		if !strings.HasPrefix(option, "--") && strings.ContainsAny(option[1:], "iI") {
			return true
		}
	}
	return false
}

// patchModeOptions reports the interactive patch mode (-p, --patch, alone or in a bundle) of
// git checkout or git restore.
func patchModeOptions(options []string) bool {
	for _, option := range options {
		if option == "--patch" || !strings.HasPrefix(option, "--") && strings.HasPrefix(option, "-") && strings.Contains(option[1:], "p") {
			return true
		}
	}
	return false
}

// patchMode is patchModeOptions over option words.
func patchMode(words []shellWord) bool {
	options := make([]string, 0, len(words))
	for _, w := range words {
		options = append(options, w.text)
	}
	return patchModeOptions(options)
}

// optionsPrefix is the leading option words of words (up to the first word not starting
// with -, or a --).
func optionsPrefix(words []shellWord) []shellWord {
	for i, w := range words {
		if w.text == "--" || !strings.HasPrefix(w.text, "-") {
			return words[:i]
		}
	}
	return words
}

// commitLine is git commit's first output line: "[branch hash] subject", the branch possibly
// followed by "(root-commit)".
var commitLine = regexp.MustCompile(`(?m)^\[[^\]\s]+(?: \([^)]*\))? ([0-9a-f]{7,40})\] (.+)$`)

// isGitCommit reports a command that runs git commit.
var isGitCommit = regexp.MustCompile(`(?:^|&&\s*|\s)git\s+commit\b`)

// commitFrom is the hash and subject of a successful git commit, read from its result; ok is
// false when the command is not a commit or the result names none.
func commitFrom(command, result string) (hash, subject string, ok bool) {
	if !isGitCommit.MatchString(command) {
		return "", "", false
	}
	m := commitLine.FindStringSubmatch(result)
	if m == nil {
		return "", "", false
	}
	return m[1], strings.TrimSpace(m[2]), true
}
