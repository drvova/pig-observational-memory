package curated

import (
	"errors"
	"fmt"
	"strings"
)

// The agent's memory tools. Each handler takes the decoded tool parameters
// and returns the text the agent sees; errors come back as text too, so the
// agent can correct the call.
const (
	ToolSave     = "memory_save"
	ToolEdit     = "memory_edit"
	ToolForget   = "memory_forget"
	ToolRead     = "memory_read"
	ToolSearch   = "memory_search"
	ToolFeedback = "memory_feedback"
)

// ToolSpec describes one tool for registration.
type ToolSpec struct {
	Name, Label, Description string
	Parameters               map[string]any
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

var nameParam = str("The memory's name: lowercase letters, digits and hyphens, e.g. prefers-tabs.")

// Tools are the memory tools' specs.
var Tools = []ToolSpec{
	{Name: ToolSave, Label: "Save memory", Description: "Save a new long-lived memory that later sessions will see in their memory index. Use it when the user corrects you, states a preference, or you learn something about this project worth keeping. One memory per fact; a taken name is refused, so update an existing memory with memory_edit instead.",
		Parameters: map[string]any{"type": "object", "required": []any{"name", "type", "description"}, "properties": map[string]any{
			"name":        nameParam,
			"type":        map[string]any{"type": "string", "enum": []any{TypeUser, TypeFeedback, TypeProject, TypeReference, TypeLesson}, "description": "user: who the user is and what they want; feedback: a correction or instruction from the user; project: a fact about this project; reference: where to find something; lesson: something you learned the hard way."},
			"description": str("One declarative sentence stating the memory."),
			"body":        str("Optional: why it matters and how to apply it."),
			"scope":       map[string]any{"type": "string", "enum": []any{"global", "project"}, "description": "global (default) applies everywhere; project applies only in this project."},
		}}},
	{Name: ToolEdit, Label: "Edit memory", Description: "Change a memory that turned out stale, wrong or incomplete. Fields you omit stay as they are.",
		Parameters: map[string]any{"type": "object", "required": []any{"name"}, "properties": map[string]any{
			"name": nameParam, "type": map[string]any{"type": "string", "enum": []any{TypeUser, TypeFeedback, TypeProject, TypeReference, TypeLesson}},
			"description": str("The new one-sentence statement."), "body": str("The new body; an empty string clears it."),
		}}},
	{Name: ToolForget, Label: "Forget memory", Description: "Remove a memory that no longer holds.",
		Parameters: map[string]any{"type": "object", "required": []any{"name"}, "properties": map[string]any{"name": nameParam}}},
	{Name: ToolRead, Label: "Read memory", Description: "Read a memory in full: its statement, body, scope and history.",
		Parameters: map[string]any{"type": "object", "required": []any{"name"}, "properties": map[string]any{"name": nameParam}}},
	{Name: ToolSearch, Label: "Search memories", Description: "Find memories beyond the index by words in their name, statement or body.",
		Parameters: map[string]any{"type": "object", "required": []any{"query"}, "properties": map[string]any{"query": str("Words that must all appear.")}}},
	{Name: ToolFeedback, Label: "Memory feedback", Description: "Record that a memory helped you or misled you. Helpful memories rise in the index; misleading ones sink, and should usually be edited or forgotten.",
		Parameters: map[string]any{"type": "object", "required": []any{"name", "outcome"}, "properties": map[string]any{
			"name": nameParam, "outcome": map[string]any{"type": "string", "enum": []any{"helped", "harmed"}},
		}}},
}

// Call is one tool invocation's context.
type Call struct {
	Store   Store
	Project string
	Source  string
}

func param(params map[string]any, key string) (string, bool) {
	v, ok := params[key].(string)
	return v, ok
}

// Run executes tool with params and returns the agent-facing text.
func (c Call) Run(tool string, params map[string]any) (string, error) {
	name, _ := param(params, "name")
	switch tool {
	case ToolSave:
		t, _ := param(params, "type")
		desc, _ := param(params, "description")
		body, _ := param(params, "body")
		m := Memory{Name: name, Type: t, Description: desc, Body: strings.TrimSpace(body)}
		if scope, _ := param(params, "scope"); scope == "project" {
			if c.Project == "" {
				return "", errors.New("no project directory is known; save it as global instead")
			}
			m.Project = c.Project
		}
		saved, err := c.Store.Save(m, c.Source)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Saved memory %s [%s]%s: %s", saved.Name, saved.Type, scopeNote(saved), saved.Description), nil
	case ToolEdit:
		edit := Edit{}
		if v, ok := param(params, "type"); ok {
			edit.Type = &v
		}
		if v, ok := param(params, "description"); ok {
			edit.Description = &v
		}
		if v, ok := param(params, "body"); ok {
			edit.Body = &v
		}
		if edit.Type == nil && edit.Description == nil && edit.Body == nil {
			return "", errors.New("nothing to change: give a new type, description or body")
		}
		m, err := c.Store.Update(name, edit, c.Source)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Updated memory %s [%s]: %s", m.Name, m.Type, m.Description), nil
	case ToolForget:
		if err := c.Store.Forget(name); err != nil {
			return "", err
		}
		return "Forgot memory " + name + ".", nil
	case ToolRead:
		m, err := c.Store.Get(name)
		if err != nil {
			return "", err
		}
		_ = c.Store.Record(name, EventView)
		return Describe(m), nil
	case ToolSearch:
		query, _ := param(params, "query")
		found, err := c.Store.Search(c.Project, query)
		if err != nil {
			return "", err
		}
		if len(found) == 0 {
			return "No memory matches " + fmt.Sprintf("%q", query) + ".", nil
		}
		lines := []string{}
		for _, m := range found {
			lines = append(lines, IndexLine(m))
		}
		return strings.Join(lines, "\n"), nil
	case ToolFeedback:
		outcome, _ := param(params, "outcome")
		if outcome != EventHelped && outcome != EventHarmed {
			return "", errors.New(`outcome must be "helped" or "harmed"`)
		}
		if _, err := c.Store.Get(name); err != nil {
			return "", err
		}
		if err := c.Store.Record(name, outcome); err != nil {
			return "", err
		}
		return "Recorded that memory " + name + " " + outcome + ".", nil
	}
	return "", fmt.Errorf("unknown memory tool %q", tool)
}

func scopeNote(m Memory) string {
	if m.Project != "" {
		return " (this project)"
	}
	return ""
}

// Describe renders a memory in full for memory_read and /om:memory.
func Describe(m Memory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s]%s: %s\n", m.Name, m.Type, scopeNote(m), m.Description)
	if m.Body != "" {
		b.WriteString("\n" + m.Body + "\n")
	}
	fmt.Fprintf(&b, "\nsaved %s, updated %s", m.Created.Format("2006-01-02"), m.Updated.Format("2006-01-02"))
	if len(m.Sources) > 0 {
		fmt.Fprintf(&b, ", sessions: %s", strings.Join(m.Sources, ", "))
	}
	return b.String()
}
