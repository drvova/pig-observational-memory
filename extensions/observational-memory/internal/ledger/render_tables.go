package ledger

import (
	"strings"
	"unicode"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
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
	var body string
	ok := false
	// A summary written before the code tool ends its instructions with the frozen paragraph
	// of the query language, and one written by the code tool's first build with the frozen
	// paragraph of its first placeholders.
	for _, p := range []string{TablesRecallParagraph, FirstCodeTablesRecallParagraph, LegacyTablesRecallParagraph} {
		if _, body, ok = strings.Cut(summary, p); ok {
			break
		}
	}
	if !ok {
		return "", false
	}
	// The files and commits lines follow the tables after a "---" line; cut them off first,
	// so a summary with no tables (only a capped list) does not read its trailer as tables.
	body, _, _ = strings.Cut(body, "\n\n---\n\n")
	// A summary that leaves records out says so in one line before its tables.
	if rest, noted := strings.CutPrefix(strings.TrimSpace(body), OmittedNoticeStart); noted {
		_, body, _ = strings.Cut(rest, "\n\n")
	}
	return strings.TrimSpace(body), true
}

// OmittedNoticeStart opens the line a memory v2 summary prints between its instructions and its
// tables when it leaves records out (selection's omitted notice); TablesSummaryBody skips that
// line. Once printed into a summary it is frozen.
const OmittedNoticeStart = "Shown here: "

// TablesRecallParagraph is the last paragraph of TablesUsageInstructions. It
// names the tool the extension registers and the om snippets that read a record
// back and search the condensed text (om.SnippetRecall, om.SnippetHistorySearch;
// omtool.ToolName, which this package does not import; omtool's tests check the
// name).
const TablesRecallParagraph = "The memory tool runs Go code: it reads any record back with its exact source, omitted ones included (" + om.SnippetRecall + "), and searches what these tables condensed (" + om.SnippetHistorySearch + "); use it when precision needs the source."

// FirstCodeTablesRecallParagraph is TablesRecallParagraph as the code tool's first build
// wrote it, before its placeholders took the <what goes there> form. It is frozen, never
// edited, for the same reason as LegacyTablesRecallParagraph.
const FirstCodeTablesRecallParagraph = "The memory tool runs Go code: it reads any record back with its exact source, omitted ones included (om.Recall(`id`)), and searches what these tables condensed (om.History(om.Search(`w`))); use it when precision needs the source."

// LegacyTablesRecallParagraph is TablesRecallParagraph as summaries wrote it
// before the code tool (docs/code-tool.md, "Migration"). It is frozen, never
// edited: TablesSummaryBody still finds the tables of a summary written then, so
// its omitted spans keep classifying hits as omitted.
const LegacyTablesRecallParagraph = `The memory tool reads any record back with its exact source, omitted ones included (recall <id>), and searches what these tables condensed (history | search "words"); use it when precision needs the source.`

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
