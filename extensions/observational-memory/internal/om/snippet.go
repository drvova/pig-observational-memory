package om

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ids"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
)

// The snippet formatter (docs/code-tool.md, "The summary's snippets and their test"): the one
// place that writes a read as Go text the agent can run, used for a list's Next page, a recall's
// next page, the retired defined queries' equivalents and the legacy-query hints. Every snippet
// is a call of a read function with its options chained as methods, so it holds no comma
// between options; an id is a Go raw string.

// The fixed snippet forms the summaries, the index and the tool's prompt lines name. Once
// printed into a summary on a branch these names are frozen. Every value in them is a
// placeholder of one form, a raw string holding <what goes there>, which no generated id, word
// list or name takes: a fixed form run as it stands is refused with PlaceholderError, while the
// snippets the formatter prints from real ids run.
const (
	SnippetRecall        = "om.Recall(`" + PlaceholderID + "`)"
	SnippetRecallEntry   = "om.Recall(`" + PlaceholderEntryID + "`)"
	SnippetRecallName    = "om.Recall(`" + PlaceholderName + "`)"
	SnippetHistorySearch = "om.History(om.Search(`" + PlaceholderWords + "`))"
	SnippetSavedSearch   = "om.Saved(om.Search(`" + PlaceholderWords + "`))"
	SnippetNotesSpan     = "om.Notes(om.From(`" + PlaceholderFirstID + "`).Through(`" + PlaceholderLastID + "`))"
	SnippetOptionChain   = "om.From(`" + PlaceholderFirstID + "`).Through(`" + PlaceholderLastID + "`).Limit(50)"
	SnippetSave          = "om.Save(`" + PlaceholderName + "`, `" + PlaceholderType + "`, `" + PlaceholderDescription + "`, om.Body(`" + PlaceholderBody + "`))"
	SnippetFeedback      = "om.Feedback(`" + PlaceholderName + "`, `" + PlaceholderOutcome + "`, `" + PlaceholderQuote + "`)"
)

// The placeholders of the fixed texts, each naming what goes in its place.
const (
	PlaceholderID      = "<id>"
	PlaceholderEntryID = "<entry id>"
	PlaceholderName    = "<name>"
	PlaceholderWords   = "<words>"
	PlaceholderFirstID = "<first id>"
	PlaceholderLastID  = "<last id>"

	PlaceholderType        = "<type>"
	PlaceholderDescription = "<description>"
	PlaceholderBody        = "<body>"
	PlaceholderOutcome     = "<outcome>"
	PlaceholderQuote       = "<quote>"

	PlaceholderString     = "<string name>"
	PlaceholderDefinition = "<definition>"
)

// placeholderWhat is, for each placeholder, what the code should hold instead.
var placeholderWhat = map[string]string{
	PlaceholderID:      "a record, reflection or entry id from the summary's lines",
	PlaceholderEntryID: "an entry id from a summary line's source",
	PlaceholderName:    "a saved memory's name from the index",
	PlaceholderWords:   "the words to search for",
	PlaceholderFirstID: "the span's first entry or record id",
	PlaceholderLastID:  "the span's last entry or record id",

	PlaceholderType:        "a memory type: user, feedback, project, reference or lesson",
	PlaceholderDescription: "a one-line description",
	PlaceholderBody:        "the memory's text: why it holds and how to apply it",
	PlaceholderOutcome:     "helped or harmed",
	PlaceholderQuote:       "the user's or assistant's words that show it",

	PlaceholderString:     "a name from the strings argument",
	PlaceholderDefinition: "a kept definition's name, as kept() lists it",
}

// IsPlaceholder reports whether s is one of the fixed texts' placeholders.
func IsPlaceholder(s string) bool {
	_, ok := placeholderWhat[s]
	return ok
}

// PlaceholderError is the refusal of a call that passes a placeholder of the fixed texts as a
// value, as running a fixed snippet form unchanged does.
func PlaceholderError(p string) error {
	return fmt.Errorf("`%s` %s, not a value: replace it with %s", p, placeholderPhrase, placeholderWhat[p])
}

// placeholderPhrase is what every PlaceholderError says.
const placeholderPhrase = "is a placeholder from the memory tool's instructions"

// NamesPlaceholder reports whether an error message holds a PlaceholderError, which is answered
// without a manual section: the code is right but for the placeholder.
func NamesPlaceholder(msg string) bool { return strings.Contains(msg, placeholderPhrase) }

// ReadFuncs are the read functions a snippet may call: the ones taking only options, and Recall,
// which takes an id first.
var ReadFuncs = []string{"Notes", "Reflections", "History", "Files", "Commits", "Attempts", "Dropped", "Saved", "Layer", "Retired", "Recall"}

// SnippetTails are the fields a strict snippet may select from its read's result.
var SnippetTails = []string{"Matched"}

// FormatRead writes the read fn with options o as a snippet, over values the extension produced
// itself (ids of the ledger and the branch): an id or date is a raw string, a word one too unless
// it holds a backtick or a carriage return, then strconv.Quote. id is Recall's id, "" for
// another read.
func FormatRead(fn, id string, o omwire.Options) string {
	s, _ := formatRead(fn, id, o, "", false)
	return s
}

// FormatReadStrict writes the read fn with options o as a snippet from values a model supplied
// (a legacy query's words and ids): every id must match the generated id formats (ids.Valid),
// a curated name the name format, every date be YYYY-MM-DD, a limit 1 to MaxLimit and a field
// a lowercase word; every word is written with strconv.Quote, never inside a raw string. tail,
// when not "", is one of SnippetTails selected from the result. The text is parsed back and
// must be a call of a read function whose options are om option calls and Opt methods with
// literal arguments, or an error says why there is no snippet.
func FormatReadStrict(fn, id string, o omwire.Options, tail string) (string, error) {
	s, err := formatRead(fn, id, o, tail, true)
	if err != nil {
		return "", err
	}
	if err := CheckSnippet(s); err != nil {
		return "", err
	}
	return s, nil
}

var (
	snippetDate  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	snippetField = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	snippetName  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
)

type snippetWriter struct {
	strict bool
	err    error
	parts  []string
}

func (w *snippetWriter) fail(format string, args ...any) {
	if w.err == nil {
		w.err = fmt.Errorf(format, args...)
	}
}

// raw is a value as a Go raw string, else quoted.
func raw(s string) string {
	if strings.ContainsAny(s, "`\r") {
		return strconv.Quote(s)
	}
	return "`" + s + "`"
}

func (w *snippetWriter) id(s string) string {
	if w.strict && !ids.Valid(s) {
		w.fail("%q is not a record or entry id", s)
	}
	return raw(s)
}

func (w *snippetWriter) date(s string) string {
	if w.strict && !snippetDate.MatchString(s) {
		w.fail("%q is not a YYYY-MM-DD date", s)
	}
	return raw(s)
}

func (w *snippetWriter) word(s string) string {
	if w.strict {
		return strconv.Quote(s)
	}
	return raw(s)
}

func (w *snippetWriter) field(s string) string {
	if w.strict && !snippetField.MatchString(s) {
		w.fail("%q is not a field name", s)
	}
	return w.word(s)
}

func (w *snippetWriter) add(method string, args ...string) {
	w.parts = append(w.parts, method+"("+strings.Join(args, ", ")+")")
}

func formatRead(fn, id string, o omwire.Options, tail string, strict bool) (string, error) {
	if !slices.Contains(ReadFuncs, fn) {
		return "", fmt.Errorf("%s is not a read", fn)
	}
	if tail != "" && !slices.Contains(SnippetTails, tail) {
		return "", fmt.Errorf("%s is not a field a snippet selects", tail)
	}
	w := &snippetWriter{strict: strict}
	for _, s := range o.Search {
		w.add("Search", w.word(s))
	}
	for _, p := range o.Where {
		if p.Field == "kind" && !strict {
			// Kind(k) is Where("kind", k), written without a comma so a snippet the extension
			// prints into a summary survives every codec as printed.
			w.add("Kind", w.word(p.Value))
			continue
		}
		w.add("Where", w.field(p.Field), w.word(p.Value))
	}
	for _, p := range o.Contains {
		w.add("Contains", w.field(p.Field), w.word(p.Value))
	}
	if o.Subject != "" {
		w.add("Subject", w.word(o.Subject))
	}
	if o.Status != "" {
		w.add("WithStatus", w.word(o.Status))
	}
	if o.From != "" {
		w.add("From", w.id(o.From))
	}
	if o.Through != "" {
		w.add("Through", w.id(o.Through))
	}
	if o.Since != "" {
		w.add("Since", w.date(o.Since))
	}
	if o.Until != "" {
		w.add("Until", w.date(o.Until))
	}
	if o.ByEvent {
		w.add("ByEvent")
	}
	if o.AsOf {
		w.add("AsOf")
	}
	if o.SortField != "" {
		w.add("SortBy", w.field(o.SortField), strconv.FormatBool(o.SortDesc))
	}
	if o.Limit != nil {
		if strict && (*o.Limit < 1 || *o.Limit > MaxLimit) {
			w.fail("limit %d is outside 1 to %d", *o.Limit, MaxLimit)
		}
		w.add("Limit", strconv.Itoa(*o.Limit))
	}
	if o.Offset != nil {
		if fn != "Recall" || *o.Offset < 0 {
			w.fail("an offset pages a recall only")
		}
		w.add("Offset", strconv.Itoa(*o.Offset))
	}
	if o.Failed {
		w.add("Failed")
	}
	if o.Worked {
		w.add("Worked")
	}
	if o.Open {
		w.add("Open")
	}
	var args []string
	if fn == "Recall" {
		if strict && !ids.Valid(id) && !snippetName.MatchString(id) {
			w.fail("%q is not a record id, an entry id or a saved name", id)
		}
		args = append(args, raw(id))
	} else if id != "" {
		w.fail("%s takes no id", fn)
	}
	if w.err != nil {
		return "", w.err
	}
	if len(w.parts) > 0 {
		args = append(args, "om."+strings.Join(w.parts, "."))
	}
	s := "om." + fn + "(" + strings.Join(args, ", ") + ")"
	if tail != "" {
		s += "." + tail
	}
	return s, nil
}

// optionFuncs are the option functions a snippet's chain may start with, and its methods.
var optionFuncs = []string{"Search", "From", "Through", "Since", "Until", "ByEvent", "AsOf", "Where", "Contains", "Kind", "Subject", "WithStatus", "SortBy", "Limit", "Offset", "Failed", "Worked", "Open"}

// errSnippetShape is a snippet that is not one read call with literal options.
var errSnippetShape = errors.New("not a read call with literal options")

// CheckSnippet reports whether s parses as one call of a read function (ReadFuncs), optionally
// followed by one of SnippetTails, whose arguments are literals (Recall's id) and at most one
// chain of option calls (om.Search(...).Limit(...)) whose arguments are literals.
func CheckSnippet(s string) error {
	expr, err := parser.ParseExpr(s)
	if err != nil {
		return fmt.Errorf("the snippet does not parse: %w", err)
	}
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		if !slices.Contains(SnippetTails, sel.Sel.Name) {
			return errSnippetShape
		}
		expr = sel.X
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok || call.Ellipsis.IsValid() {
		return errSnippetShape
	}
	fn, ok := omSelector(call.Fun)
	if !ok || !slices.Contains(ReadFuncs, fn) {
		return errSnippetShape
	}
	args := call.Args
	if fn == "Recall" {
		if len(args) == 0 || !isLiteral(args[0]) {
			return errSnippetShape
		}
		args = args[1:]
	}
	if len(args) > 1 {
		return errSnippetShape
	}
	for _, a := range args {
		if !isOptionChain(a) {
			return errSnippetShape
		}
	}
	return nil
}

// omSelector is the name om.Name selects, false for anything else.
func omSelector(e ast.Expr) (string, bool) {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return sel.Sel.Name, ok && pkg.Name == "om"
}

// isOptionChain reports whether e is om.Opt(lits...) followed by .Opt(lits...) calls.
func isOptionChain(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok || call.Ellipsis.IsValid() {
		return false
	}
	for _, a := range call.Args {
		if !isLiteral(a) {
			return false
		}
	}
	if name, ok := omSelector(call.Fun); ok {
		return slices.Contains(optionFuncs, name)
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !slices.Contains(optionFuncs, sel.Sel.Name) {
		return false
	}
	return isOptionChain(sel.X)
}

// isLiteral reports whether e is a string or integer literal, or true or false.
func isLiteral(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.BasicLit:
		return v.Kind == token.STRING || v.Kind == token.INT
	case *ast.Ident:
		return v.Name == "true" || v.Name == "false"
	}
	return false
}

// isIdentifier reports whether s is a Go identifier.
func isIdentifier(s string) bool {
	for i, r := range s {
		if !(unicode.IsLetter(r) || r == '_' || i > 0 && unicode.IsDigit(r)) {
			return false
		}
	}
	return s != "" && !token.IsKeyword(s)
}

// IsStringName reports whether name may name one of a call's strings: a Go identifier.
func IsStringName(name string) bool { return isIdentifier(name) }
