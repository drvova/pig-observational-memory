package extract

import (
	"regexp"
	"slices"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ids"
	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// Repository conventions (docs/memory-v2.md, "Repository conventions"): what a repository file
// the session read sets, recorded as an observation of that file, never as an instruction. Only
// a read tool's successful result counts, so a convention is always one the session saw; the
// disk is never read. The record names the file (Observation.Target) and the SHA-256 prefix of
// what was read (ids.HashID), so a later read of changed content supersedes it.

// conventionManagers are the lockfiles, by base name, and the package manager each implies.
var conventionManagers = map[string]string{
	"package-lock.json": "npm", "npm-shrinkwrap.json": "npm", "yarn.lock": "yarn", "pnpm-lock.yaml": "pnpm",
	"bun.lock": "bun", "bun.lockb": "bun", "Cargo.lock": "cargo", "go.sum": "go modules", "poetry.lock": "poetry",
	"uv.lock": "uv", "Pipfile.lock": "pipenv", "Gemfile.lock": "bundler", "composer.lock": "composer",
}

// conventionTools are the formatter and linter configs, by base name, and what each names.
var conventionTools = map[string]string{
	".prettierrc": "formatter prettier", ".prettierrc.json": "formatter prettier", ".prettierrc.yaml": "formatter prettier",
	".prettierrc.yml": "formatter prettier", ".prettierrc.js": "formatter prettier", ".prettierrc.cjs": "formatter prettier",
	".prettierrc.mjs": "formatter prettier", ".prettierrc.toml": "formatter prettier", "prettier.config.js": "formatter prettier",
	"prettier.config.cjs": "formatter prettier", "prettier.config.mjs": "formatter prettier",
	".eslintrc": "linter eslint", ".eslintrc.json": "linter eslint", ".eslintrc.js": "linter eslint", ".eslintrc.cjs": "linter eslint",
	".eslintrc.yaml": "linter eslint", ".eslintrc.yml": "linter eslint", "eslint.config.js": "linter eslint",
	"eslint.config.mjs": "linter eslint", "eslint.config.cjs": "linter eslint", "eslint.config.ts": "linter eslint",
	"ruff.toml": "linter ruff", ".ruff.toml": "linter ruff", "rustfmt.toml": "formatter rustfmt", ".rustfmt.toml": "formatter rustfmt",
	".golangci.yml": "linter golangci-lint", ".golangci.yaml": "linter golangci-lint", ".golangci.toml": "linter golangci-lint",
	"go.mod": "formatter gofmt (implied by go.mod)",
}

var (
	// conventionLineNumber is the line number a read tool may print before each line ("  12→",
	// "  12\t"), taken off before a line is parsed (never before hashing).
	conventionLineNumber = regexp.MustCompile(`^\s*\d+(?:→|\t)`)
	// editorconfigSetting is one .editorconfig setting a convention keeps.
	editorconfigSetting = regexp.MustCompile(`^\s*(indent_style|indent_size|tab_width|end_of_line|charset|max_line_length|insert_final_newline|trim_trailing_whitespace)\s*=\s*(\S+)`)
	// pyprojectTool is a pyproject.toml table of a formatter or linter.
	pyprojectTool = regexp.MustCompile(`^\s*\[tool\.(ruff|black)\b`)
	// ciRun is a workflow step's run key, with its indentation and value.
	ciRun = regexp.MustCompile(`^(\s*)(?:-\s+)?run:\s*(.*)$`)
	// makeTarget is a Makefile rule line's targets: names only, never a variable assignment
	// (":=", "::="), a pattern rule or a file path.
	makeTarget = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_-]*(?:[ \t]+[A-Za-z0-9][A-Za-z0-9_-]*)*)[ \t]*::?(?:[^=]|$)`)
)

// toolResult reads a read tool's result for a repository convention (conventionOf): the call it
// answers is looked up before it, back to the user message that opened its exchange.
func (x *extraction) toolResult(i int) {
	message := x.in.Entries[i].Message
	if failed, _ := message["isError"].(bool); failed {
		return
	}
	id, _ := message["toolCallId"].(string)
	call, at, ok := x.callOf(i, id)
	if !ok || x.isSkipped(call.name) || fileToolState(call) != stateRead {
		return
	}
	p := ""
	for _, arg := range pathArgs {
		if v, ok := call.args[arg].(string); ok && strings.TrimSpace(v) != "" {
			p = v
			break
		}
	}
	text := textBlocks(message["content"])
	if p == "" || strings.TrimSpace(text) == "" {
		return
	}
	display := x.pathDisplay(p)
	what := conventionOf(display, text)
	if what == "" {
		return
	}
	content := what + " (repo file `" + display + "`, sha256 " + ids.HashID(text) + ")"
	earlier := x.conventions[display]
	for _, c := range earlier {
		if c.content == content {
			return
		}
	}
	obs := ledger.Observation{Content: content, Kind: ledger.KindConvention, Tool: call.name, Target: display}
	for _, c := range earlier {
		obs.Supersedes = append(obs.Supersedes, c.id)
		x.superseded[c.id] = true
	}
	r := x.add(obs, at, i)
	r.obs.ID = recordID(r.obs)
	x.conventions[display] = []convention{{r.obs.ID, r.obs.Content}}
}

// convention is an active convention record of one file.
type convention struct{ id, content string }

// callOf is the tool call id names, in an assistant message before the result at i and after
// the user message before it, with the call's branch index.
func (x *extraction) callOf(i int, id string) (toolCall, int, bool) {
	if id == "" {
		return toolCall{}, 0, false
	}
	for j := i - 1; j >= 0; j-- {
		entry := x.in.Entries[j]
		if entry.Type != "message" || entry.Message == nil {
			continue
		}
		switch entry.Message["role"] {
		case "user":
			return toolCall{}, 0, false
		case "assistant":
			blocks, _ := entry.Message["content"].([]any)
			for _, raw := range blocks {
				block, _ := raw.(map[string]any)
				if block == nil || block["type"] != "toolCall" || block["id"] != id {
					continue
				}
				name, _ := block["name"].(string)
				args, _ := block["arguments"].(map[string]any)
				return toolCall{id: id, name: name, args: args, raw: block["arguments"]}, j, true
			}
		}
	}
	return toolCall{}, 0, false
}

// conventionOf is what a file the session read at path sets, "" when it is no repository
// convention file or sets nothing: a lockfile's package manager, a formatter or linter config,
// the settings of an .editorconfig, a pyproject.toml's formatter and linter tables, a GitHub
// workflow's run lines, a Makefile's targets.
func conventionOf(path, text string) string {
	base := path[strings.LastIndex(path, "/")+1:]
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for n, line := range lines {
		lines[n] = conventionLineNumber.ReplaceAllString(line, "")
	}
	switch {
	case conventionManagers[base] != "":
		return "package manager " + conventionManagers[base]
	case conventionTools[base] != "":
		return conventionTools[base]
	case base == ".editorconfig":
		settings := []string{}
		for _, line := range lines {
			if m := editorconfigSetting.FindStringSubmatch(line); m != nil && !slices.Contains(settings, m[1]+"="+m[2]) {
				settings = append(settings, m[1]+"="+m[2])
			}
		}
		if len(settings) == 0 {
			return "editorconfig"
		}
		return "editorconfig " + strings.Join(settings, ", ")
	case base == "pyproject.toml":
		parts := []string{}
		for _, line := range lines {
			if m := pyprojectTool.FindStringSubmatch(line); m != nil {
				part := "linter ruff"
				if m[1] == "black" {
					part = "formatter black"
				}
				if !slices.Contains(parts, part) {
					parts = append(parts, part)
				}
			}
		}
		return strings.Join(parts, "; ")
	case strings.Contains("/"+path, "/.github/workflows/") && (strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml")):
		commands := ciCommands(lines)
		if len(commands) == 0 {
			return ""
		}
		return "CI runs `" + strings.Join(commands, "`; `") + "`"
	case base == "Makefile" || base == "makefile" || base == "GNUmakefile" || strings.HasSuffix(base, ".mk"):
		targets := []string{}
		for _, line := range lines {
			if m := makeTarget.FindStringSubmatch(line); m != nil {
				for _, t := range strings.Fields(m[1]) {
					if !slices.Contains(targets, t) {
						targets = append(targets, t)
					}
				}
			}
		}
		if len(targets) == 0 {
			return ""
		}
		return "make targets " + strings.Join(targets, ", ")
	}
	return ""
}

// ciCommands are a workflow's run commands in order, without duplicates: a run key's value, or
// each line of its block scalar (| or >), the lines indented deeper than the key's line.
func ciCommands(lines []string) []string {
	out := []string{}
	add := func(c string) {
		if c = strings.TrimSpace(c); c != "" && !strings.HasPrefix(c, "#") && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	for n := 0; n < len(lines); n++ {
		m := ciRun.FindStringSubmatch(lines[n])
		if m == nil {
			continue
		}
		value := strings.TrimSpace(m[2])
		switch value {
		case "|", "|-", "|+", ">", ">-", ">+":
			indent := len(m[1])
			for n+1 < len(lines) {
				next := lines[n+1]
				if strings.TrimSpace(next) != "" && len(next)-len(strings.TrimLeft(next, " \t")) <= indent {
					break
				}
				n++
				add(next)
			}
		default:
			if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
				value = value[1 : len(value)-1]
			}
			add(value)
		}
	}
	return out
}
