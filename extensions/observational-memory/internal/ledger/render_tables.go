package ledger

import (
	"strings"
	"unicode"
)

// TablesSummaryIntro starts the instructions of every tables summary, whatever
// its codec, so a reader can find a summary's tables without knowing the codec.
const TablesSummaryIntro = "These are condensed memories from earlier in this session, as tables"

// TablesSummaryBody is the encoded tables of a tables summary in any codec:
// the text after its instructions, up to any appended section ("\n\n---\n\n").
// False when summary is not a tables summary.
func TablesSummaryBody(summary string) (string, bool) {
	if !strings.HasPrefix(summary, TablesSummaryIntro) {
		return "", false
	}
	_, body, ok := strings.Cut(summary, TablesRecallParagraph)
	if !ok {
		return "", false
	}
	body, _, _ = strings.Cut(body, "\n\n---\n\n")
	return strings.TrimSpace(body), true
}

// TablesRecallParagraph is the last paragraph of TablesUsageInstructions. It
// names the tool the extension registers and its queries (memquery.ToolName,
// which this package cannot import; memquery's tests check the name).
const TablesRecallParagraph = `The memory tool reads any record back with its exact source, omitted ones included (recall <id>), and searches what these tables condensed (history | search "words"); use it when precision needs the source.`

// Table names and fields of the tables format.
const (
	reflectionsTable = "reflections"
	notesTable       = "notes"
	omittedTable     = "omitted"
)

var ()

// SubjectTableName is the table an observation with subject is listed in:
// "notes." and the subject lowercased, each run of characters a table name
// cannot hold (see tables.ValidName) replaced by "_". It is "" for a subject
// with no letters or digits.
func SubjectTableName(subject string) string {
	var b strings.Builder
	pending := false
	for _, r := range strings.ToLower(subject) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '.' {
			if pending && b.Len() > 0 {
				b.WriteByte('_')
			}
			pending = false
			b.WriteRune(r)
			continue
		}
		pending = true
	}
	name := strings.Trim(b.String(), "-.")
	if name == "" {
		return ""
	}
	return notesTable + "." + name
}

func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
