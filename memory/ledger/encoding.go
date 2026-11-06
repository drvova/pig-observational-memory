package ledger

import (
	"slices"
	"strings"
)

// The encoding fields of a record (Observation.Source, Authority, Hedge, TimeHint, Keys, Frame,
// Writer; docs/brain-memory.md, "Encoding"). Code sets every one at write time; contracts/encoding.md
// is the language-neutral rule both engines follow, with its cases in contracts/encoding.jsonl.

// The sources of a record (Observation.Source).
const (
	// SourceUserSaid is the person's own words: a user message.
	SourceUserSaid = "user-said"
	// SourceToolObserved is what a tool call showed: an attempt, a file state, a commit, a user's
	// shell command, a subagent's result.
	SourceToolObserved = "tool-observed"
	// SourceAssistantClaim is what the session model said: its replies, its words on a tool call,
	// its memory tool notes.
	SourceAssistantClaim = "assistant-claim"
	// SourceModelInferred is what level 1's request wrote: a merge, or a relabel whose kind the
	// model chose.
	SourceModelInferred = "model-inferred"
	// SourceHostSummary is a compaction summary the ledger does not back (OriginHost).
	SourceHostSummary = "host-summary"
	// SourceHarnessMemory is a harness's own memory file (CLAUDE.md and the like).
	SourceHarnessMemory = "harness-memory"
	// SourceOtherSession is a message another session sent (Claude Code's origin "peer").
	SourceOtherSession = "other-session"
	// SourceExternalUntrusted is content from outside the session no other source names.
	SourceExternalUntrusted = "external-untrusted"
)

// SourceValues are the valid record sources.
var SourceValues = []string{SourceUserSaid, SourceToolObserved, SourceAssistantClaim, SourceModelInferred, SourceHostSummary, SourceHarnessMemory, SourceOtherSession, SourceExternalUntrusted}

// The authorities of a record (Observation.Authority), highest first.
const (
	AuthorityUser     = "user"
	AuthorityRepoFile = "repo-file"
	AuthorityTool     = "tool"
	AuthorityModel    = "model"
)

// AuthorityValues are the valid authorities, highest first.
var AuthorityValues = []string{AuthorityUser, AuthorityRepoFile, AuthorityTool, AuthorityModel}

// The hedges of a record (Observation.Hedge).
const (
	// HedgeStated is a sentence with no hedge marker.
	HedgeStated = "stated"
	// HedgeHedged is a sentence whose speaker marks their own uncertainty ("I think", "maybe").
	HedgeHedged = "hedged"
	// HedgeUnverified is hearsay: a sentence that reports what someone else said or what
	// apparently holds ("apparently", "I heard").
	HedgeUnverified = "unverified"
	// HedgeContested is a user's claim the assistant pushed back on in its reply (a pushback
	// record responds to it; Contested). The lexicon never gives it: code sets it on the claim
	// when it records the pushback.
	HedgeContested = "contested"
)

// HedgeValues are the valid hedges.
var HedgeValues = []string{HedgeStated, HedgeHedged, HedgeUnverified, HedgeContested}

// The engines that write records (Writer.Engine).
const (
	EngineGo   = "go"
	EngineLisp = "lisp"
)

// The scope forms beside a free qualifier (Observation.Scope): a record's scope may also name
// where it holds. Old free-text scopes keep loading and keep their meaning.
const (
	ScopeSession       = "session"
	ScopeProjectPrefix = "project:"
	ScopeUser          = "user"
	ScopeGlobal        = "global"
)

// ProjectScope is the scope of a record that holds in one repository (Frame.RepoID).
func ProjectScope(repoID string) string { return ScopeProjectPrefix + repoID }

// Keys are the exact strings a record can be looked up by, each list in the order code found
// them, without repeats. Code fills them from what level 0 already knows (Keys.Add).
type Keys struct {
	Paths    []string `json:"paths,omitempty"`
	Symbols  []string `json:"symbols,omitempty"`
	Commands []string `json:"commands,omitempty"`
	Tests    []string `json:"tests,omitempty"`
	Errors   []string `json:"errors,omitempty"`
	Config   []string `json:"config,omitempty"`
	Versions []string `json:"versions,omitempty"`
}

// The key kinds (Keys.Add), in the order a record's keys are written.
const (
	KeyPaths    = "paths"
	KeySymbols  = "symbols"
	KeyCommands = "commands"
	KeyTests    = "tests"
	KeyErrors   = "errors"
	KeyConfig   = "config"
	KeyVersions = "versions"
)

// KeyKinds are the key kinds in write order.
var KeyKinds = []string{KeyPaths, KeySymbols, KeyCommands, KeyTests, KeyErrors, KeyConfig, KeyVersions}

// IsZero reports keys with no value, which a record omits.
func (k Keys) IsZero() bool {
	for _, kind := range KeyKinds {
		if len(*k.list(kind)) > 0 {
			return false
		}
	}
	return true
}

func (k *Keys) list(kind string) *[]string {
	switch kind {
	case KeyPaths:
		return &k.Paths
	case KeySymbols:
		return &k.Symbols
	case KeyCommands:
		return &k.Commands
	case KeyTests:
		return &k.Tests
	case KeyErrors:
		return &k.Errors
	case KeyConfig:
		return &k.Config
	case KeyVersions:
		return &k.Versions
	}
	return nil
}

// Get is the list of one key kind, nil for an unknown kind.
func (k Keys) Get(kind string) []string {
	if l := k.list(kind); l != nil {
		return *l
	}
	return nil
}

// Add appends value to the keys of kind (KeyKinds), trimmed, unless it is empty or already
// there; an unknown kind adds nothing. The list is copied before it grows, so a record copied
// from another never changes the other's keys. This is the helper every extractor calls.
func (k *Keys) Add(kind, value string) {
	value = strings.TrimSpace(value)
	l := k.list(kind)
	if l == nil || value == "" || slices.Contains(*l, value) {
		return
	}
	*l = append(slices.Clip(*l), value)
}

// Union adds every key of other, kind by kind, in its order.
func (k *Keys) Union(other Keys) {
	for _, kind := range KeyKinds {
		for _, v := range other.Get(kind) {
			k.Add(kind, v)
		}
	}
}

// Frame is where in a repository a record was made: the repository (host.Repo's ID), its branch
// and HEAD commit when the record was written, and the repository-relative path (and, later, the
// symbol and the hash of its body) the record is about.
type Frame struct {
	RepoID   string `json:"repoId,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Head     string `json:"head,omitempty"`
	Path     string `json:"path,omitempty"`
	Symbol   string `json:"symbol,omitempty"`
	BodyHash string `json:"bodyHash,omitempty"`
}

// Writer is who wrote a record: the harness, its session id, the session model (provider/id, ""
// when unknown) and the engine (EngineGo, EngineLisp).
type Writer struct {
	Harness string `json:"harness,omitempty"`
	Session string `json:"session,omitempty"`
	Model   string `json:"model,omitempty"`
	Engine  string `json:"engine,omitempty"`
}

// AuthorityOf is the authority code gives a record of source: the user's own words are the
// user's, a harness memory file is a repository file's, what a tool showed is a tool's, and
// everything a model wrote or another session sent is a model's. An unknown source has none.
func AuthorityOf(source string) string {
	switch source {
	case SourceUserSaid:
		return AuthorityUser
	case SourceHarnessMemory:
		return AuthorityRepoFile
	case SourceToolObserved:
		return AuthorityTool
	case SourceAssistantClaim, SourceModelInferred, SourceHostSummary, SourceOtherSession, SourceExternalUntrusted:
		return AuthorityModel
	}
	return ""
}

// AuthorityRank orders authorities, user highest; an empty or unknown one (a record written
// before authority existed) ranks lowest, so it never raises what a merge inherits.
func AuthorityRank(authority string) int {
	switch authority {
	case AuthorityUser:
		return 3
	case AuthorityRepoFile:
		return 2
	case AuthorityTool:
		return 1
	case AuthorityModel:
		return 0
	}
	return -1
}

// LeastAuthority is the lower of two authorities (AuthorityRank).
func LeastAuthority(a, b string) string {
	if AuthorityRank(b) < AuthorityRank(a) {
		return b
	}
	return a
}

// HedgeRank orders hedges, contested highest, then unverified; an empty one ranks lowest.
func HedgeRank(hedge string) int {
	switch hedge {
	case HedgeContested:
		return 3
	case HedgeUnverified:
		return 2
	case HedgeHedged:
		return 1
	case HedgeStated:
		return 0
	}
	return -1
}

// StrongestHedge is the more hedged of two hedges (HedgeRank): a merge never states more surely
// than its sources.
func StrongestHedge(a, b string) string {
	if HedgeRank(b) > HedgeRank(a) {
		return b
	}
	return a
}

// SourceOfCustomType is the source of a custom message a transcript reader made of a line that is
// not the person's words (transcript.OriginCustomPrefix): another session's message is
// other-session, a subagent's result tool-observed, any other origin external-untrusted; "" for a
// custom type that is not an origin.
func SourceOfCustomType(customType string) string {
	kind, ok := strings.CutPrefix(customType, "origin:")
	switch {
	case !ok:
		return ""
	case kind == "peer":
		return SourceOtherSession
	case kind == "task-notification":
		return SourceToolObserved
	}
	return SourceExternalUntrusted
}

// unverifiedMarkers and hedgedMarkers are the hedge lexicon (contracts/encoding.md): phrases in
// ASCII lower case, matched as whole words.
var unverifiedMarkers = []string{
	"apparently", "reportedly", "supposedly", "allegedly", "hearsay",
	"i heard", "i've heard", "i have heard", "i was told", "i've been told", "i have been told", "i'm told",
	"someone said", "someone told me", "people say", "they say", "word is",
	"rumor", "rumour", "rumored", "rumoured",
}

var hedgedMarkers = []string{
	"i think", "i don't think", "i do not think", "i believe", "i guess", "i suppose", "i suspect", "i assume", "i'd say",
	"maybe", "perhaps", "possibly", "probably", "presumably", "likely", "unlikely",
	"might", "may be", "may have", "could be",
	"not sure", "unsure", "not certain", "uncertain",
	"seems", "seem to", "seemed", "it looks like", "i feel like",
	"if i recall", "iirc", "afaik", "as far as i know",
}

// pastMarkers name a past event the record reports without a date (TimeHintOf).
var pastMarkers = []string{"earlier", "previously", "used to", "already", "last time", "in the past"}

// lexiconText is text as the lexicon reads it: ASCII letters lower-cased, curly apostrophes
// straight, and every run of whitespace one space.
func lexiconText(text string) string {
	text = strings.NewReplacer("’", "'", "‘", "'").Replace(text)
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func wordByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

// hasMarker reports whether text (lexiconText) holds one of markers as whole words: the byte
// before a match is not a letter, digit or apostrophe, and the byte after is not a letter or digit.
func hasMarker(text string, markers []string) bool {
	for _, m := range markers {
		for from := 0; from <= len(text)-len(m); {
			i := strings.Index(text[from:], m)
			if i < 0 {
				break
			}
			i += from
			end := i + len(m)
			if (i == 0 || (!wordByte(text[i-1]) && text[i-1] != '\'')) && (end == len(text) || !wordByte(text[end])) {
				return true
			}
			from = i + 1
		}
	}
	return false
}

// HedgeOf is the hedge of a record's own sentence by the code lexicon (contracts/encoding.md):
// unverified when it reports hearsay, hedged when its speaker marks uncertainty, else stated.
func HedgeOf(text string) string {
	t := lexiconText(text)
	switch {
	case hasMarker(t, unverifiedMarkers):
		return HedgeUnverified
	case hasMarker(t, hedgedMarkers):
		return HedgeHedged
	}
	return HedgeStated
}

// TimeHintOf is a record's time hint (contracts/encoding.md): "after:<date>" for a plan
// (StatusProposed), "before:<date>" for a fact or assistant statement with no event date that
// reports a past event by a past marker ("earlier", "already"), "" otherwise. The date is the
// day the record was said, the first ten characters of its level-0 timestamp; none without one.
func TimeHintOf(o Observation) string {
	if len(o.Timestamp) < 10 || !isISODate(o.Timestamp[:10]) {
		return ""
	}
	date := o.Timestamp[:10]
	switch {
	case o.Status == StatusProposed:
		return "after:" + date
	case o.Status == "" && o.EventDate == "" && (o.Kind == "fact" || o.Kind == "assistant") && hasMarker(lexiconText(o.Content), pastMarkers):
		return "before:" + date
	}
	return ""
}

// isTimeHint reports a stored time hint: "after:" or "before:" and an ISO date.
func isTimeHint(v string) bool {
	date, ok := strings.CutPrefix(v, "after:")
	if !ok {
		date, ok = strings.CutPrefix(v, "before:")
	}
	return ok && isISODate(date)
}

// VersionsIn are the name@version tokens of text, in order without repeats (contracts/encoding.md):
// a whitespace-separated token, stripped of surrounding quotes and brackets and of trailing
// punctuation, whose last "@" follows a name of letters, digits and ._/@- holding a letter, and
// precedes a version that starts with a digit (or "v" and a digit) and holds only letters,
// digits and .+_-.
func VersionsIn(text string) []string {
	out := []string{}
	for _, tok := range strings.Fields(text) {
		tok = strings.TrimLeft(tok, "\"'`([{<")
		tok = strings.TrimRight(tok, "\"'`)]}>.,;:!?")
		if len(tok) > 200 {
			continue
		}
		at := strings.LastIndexByte(tok, '@')
		if at <= 0 || !versionName(tok[:at]) || !versionValue(tok[at+1:]) {
			continue
		}
		if !slices.Contains(out, tok) {
			out = append(out, tok)
		}
	}
	return out
}

func versionName(s string) bool {
	letter := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			letter = true
		case (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '/' || c == '@' || c == '-':
		default:
			return false
		}
	}
	return letter
}

func versionValue(s string) bool {
	if strings.HasPrefix(s, "v") {
		s = s[1:]
	}
	if s == "" || s[0] < '0' || s[0] > '9' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '+' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// readEncoding reads the encoding fields of a decoded record leniently, as ObservationFrom reads
// every optional member: a value outside its set, or a member of the wrong shape, is cleared.
func (observation *Observation) readEncoding(m map[string]any) {
	if s, ok := m["source"].(string); ok && slices.Contains(SourceValues, s) {
		observation.Source = s
	}
	if s, ok := m["authority"].(string); ok && slices.Contains(AuthorityValues, s) {
		observation.Authority = s
	}
	if s, ok := m["hedge"].(string); ok && slices.Contains(HedgeValues, s) {
		observation.Hedge = s
	}
	if s, ok := m["timeHint"].(string); ok && isTimeHint(s) {
		observation.TimeHint = s
	}
	if s, ok := m["episode"].(string); ok {
		observation.Episode = s
	}
	if keys, ok := m["keys"].(map[string]any); ok {
		for _, kind := range KeyKinds {
			list, _ := keys[kind].([]any)
			for _, v := range list {
				if s, ok := v.(string); ok {
					observation.Keys.Add(kind, s)
				}
			}
		}
	}
	str := func(o map[string]any, key string) string {
		s, _ := o[key].(string)
		return s
	}
	if f, ok := m["frame"].(map[string]any); ok {
		observation.Frame = Frame{RepoID: str(f, "repoId"), Branch: str(f, "branch"), Head: str(f, "head"), Path: str(f, "path"), Symbol: str(f, "symbol"), BodyHash: str(f, "bodyHash")}
	}
	if w, ok := m["writer"].(map[string]any); ok {
		observation.Writer = Writer{Harness: str(w, "harness"), Session: str(w, "session"), Model: str(w, "model"), Engine: str(w, "engine")}
	}
}
