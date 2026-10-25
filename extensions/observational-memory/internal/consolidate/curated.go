package consolidate

import (
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
)

// CuratedMemory is a curated memory the level-1 input lists (docs/memory-v2.md, level 1's
// "Input, in order"): its index line, and whether it is staged.
type CuratedMemory struct {
	Name, Type, Description string
	Staged                  bool
}

// Citation is a curated operation level 1 made: the memory named, borne out by records of the
// user's own words the input showed. A citation is evidence for a staged memory
// (curated.Store.Settle); it changes no memory's text.
type Citation struct {
	Name    string
	Records []string
}

// headingCurated opens the curated memories in the level-1 input.
const headingCurated = "CURATED MEMORY (memories saved with the memory tool for later sessions; staged ones are not yet in the memory index. Cite one with curated when shown records of the user's own words bear it out):"

// curatedLine is how the level-1 input lists a curated memory: its index line, marked when it
// is staged.
func curatedLine(m CuratedMemory) string {
	line := "- " + m.Name + " [" + m.Type + "]"
	if m.Staged {
		line += " (staged)"
	}
	return line + ": " + m.Description
}

// omittedCuratedLine closes the curated memories when n of them did not fit.
func omittedCuratedLine(n int) string {
	return fmt.Sprintf("omitted: %d more curated memories are not shown and cannot be cited.", n)
}

// The refusal reasons of a curated item.
const (
	ReasonNotCurated    = "names a curated memory the input did not list"
	ReasonNoUserSupport = "cites no shown record of the user's own words"
)

// curated validates level 1's curated operations (docs/memory-v2.md, level 1's "Output"): each
// names a curated memory the input listed, once, with supporting ids the input showed, at least
// one of them a record of the user's own words (its first source entry is a user message on the
// branch), so a citation is evidence the model did not produce itself.
func (v *validator) curated(items []any) {
	listed := map[string]bool{}
	for _, m := range v.in.Curated {
		listed[m.Name] = true
	}
	roles := entryRoles(v.in.Branch)
	cited := map[string]bool{}
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			v.refuse("curated", i, ReasonMalformed)
			continue
		}
		name, _ := m["name"].(string)
		name = strings.TrimSpace(name)
		if !listed[name] {
			v.refuse("curated", i, ReasonNotCurated)
			continue
		}
		if cited[name] {
			v.refuse("curated", i, ReasonUsed)
			continue
		}
		ids := uniqueStrings(m["supportingIds"])
		user, foreign := false, len(ids) == 0
		for _, id := range ids {
			o, shown := v.shown[id]
			if !shown {
				foreign = true
				break
			}
			user = user || fromUserMessage(o, roles)
		}
		switch {
		case foreign:
			v.refuse("curated", i, ReasonForeignID)
			continue
		case !user:
			v.refuse("curated", i, ReasonNoUserSupport)
			continue
		}
		cited[name] = true
		v.result.Cited = append(v.result.Cited, Citation{Name: name, Records: ids})
	}
}

// entryRoles are the message roles of the branch's entries, by id.
func entryRoles(entries []ledger.Entry) map[string]string {
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.Type == "message" && entry.Message != nil {
			role, _ := entry.Message["role"].(string)
			out[entry.ID] = role
		}
	}
	return out
}

// fromUserMessage reports whether code picked o from a user message: its origin is code and its
// first source entry is a user message on the branch.
func fromUserMessage(o ledger.Observation, roles map[string]string) bool {
	return o.Origin == ledger.OriginCode && len(o.SourceEntryIDs) > 0 && roles[o.SourceEntryIDs[0]] == "user"
}
