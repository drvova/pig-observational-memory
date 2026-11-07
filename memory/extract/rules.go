package extract

import (
	"regexp"
	"slices"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// The standing rules and decisions level 0 reads from fixed wording beside the preference
// constructions (docs/memory-v2.md, "Conversation facts"): the forms compaction loses most
// (scope exclusions and process rules), read from the user's own turns only.
var (
	// rulePatterns are standing rules that need no object check: the rule verbs the preference
	// lists lack after always/never, substitutions ("use X instead of Y", "use X, not Y",
	// "prefer X over Y"), standing instructions ("from now on"), process rules ("before you
	// commit", "after each change run", "make sure to ... before") and the first-person-plural
	// team forms ("we always", "our convention is").
	rulePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\balways\s+(?:touch|edit|modify|change|import|install|bump|upgrade|delete|rename|commit|push|deploy|run)\b`),
		regexp.MustCompile(`(?i)\bnever\s+(?:touch|edit|modify|change|import|install|bump|upgrade|delete|rename|commit|push|deploy|run)\b`),
		regexp.MustCompile(`(?i)\buse\s+\S.*?\s+instead\s+of\s+\S`),
		regexp.MustCompile(`(?i)\buse\s+[^,.;:!?]+,\s*not\s+\S`),
		regexp.MustCompile(`(?i)\bprefer\s+\S.*?\s+over\s+\S`),
		regexp.MustCompile(`(?i)\bfrom\s+now\s+on\b`),
		regexp.MustCompile(`(?i)\b(?:always|never)\b.*\bfrom\s+now\b`),
		regexp.MustCompile(`(?i)\bbefore\s+(?:you\s+|we\s+|i\s+)?(?:commit|push|merg)\w*`),
		regexp.MustCompile(`(?i)\bafter\s+(?:each|every)\s+\w+.*?\brun\b`),
		regexp.MustCompile(`(?i)\bmake\s+sure\s+(?:to|you|that)\b.*\bbefore\b`),
		regexp.MustCompile(`(?i)\bwe\s+(?:always|never|use|prefer)\b`),
		regexp.MustCompile(`(?i)\bour\s+convention\s+is\b`),
	}
	// scopePatterns are scope exclusions ("don't touch X", "leave X alone", "only modify X",
	// "stay out of X"), whose first group is the first word of X: one is a rule only when X
	// is more than a pronoun or the sentence quotes what it is about (ledger.RecordKeys).
	scopePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(?:don'?t|do\s+not|never)\s+(?:touch|edit|modify|change)\s+(\S+)`),
		regexp.MustCompile(`(?i)\bleave\s+(\S+)[^.;!?]*?\s+alone\b`),
		regexp.MustCompile(`(?i)\bonly\s+(?:modify|change|edit|touch)\s+(\S+)`),
		regexp.MustCompile(`(?i)\bstay\s+out\s+of\s+(\S+)`),
	}
	// ruleHypothetical states a rule under a condition the speaker poses ("if we", "what if",
	// "suppose"): it is not a standing rule. "If you change the schema, run make gen" is a rule
	// addressed to the agent, so "if you" does not count here.
	ruleHypothetical = regexp.MustCompile(`(?i)\b(?:if\s+(?:we|i)|suppose|supposing|what\s+if|hypothetically)\b`)
	// decisionPatterns are a choice stated in fixed wording: "let's go with X", "we're going
	// with X", "we'll use X because Y", "decided to/on/against X", "chose/opted for/
	// settled on X", "rejected Y because Z".
	decisionPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\blet'?s\s+go\s+with\b`),
		regexp.MustCompile(`(?i)\b(?:we'?re|we\s+are|i'?m|i\s+am)\s+going\s+with\b`),
		regexp.MustCompile(`(?i)\b(?:we'?ll|we\s+will|i'?ll|i\s+will)\s+(?:use|go\s+with|stick\s+with)\b.*\b(?:because|since)\b`),
		regexp.MustCompile(`(?i)\bdecided\s+(?:to|on|against)\b`),
		regexp.MustCompile(`(?i)\b(?:chose|opted\s+for|settled\s+on)\b`),
		regexp.MustCompile(`(?i)\brejected\b.*\bbecause\b`),
	}
	// decisionUnmade is a decision not taken: "haven't decided", "not decided yet".
	decisionUnmade = regexp.MustCompile(`(?i)(?:\bnot|n't|\bnever)\s+(?:yet\s+)?decided\b`)
	// decisionReason is a stated reason, which an assistant's decision needs to be one.
	decisionReason = regexp.MustCompile(`(?i)\b(?:because|since|so\s+that)\b`)
)

// scopePronouns are the words that leave a scope exclusion's object unnamed: "don't touch it".
var scopePronouns = map[string]bool{
	"it": true, "that": true, "this": true, "them": true, "those": true, "these": true, "me": true,
	"us": true, "him": true, "her": true, "anything": true, "everything": true, "something": true,
	"nothing": true,
}

// isRule reports a standing rule in a user sentence (rulePatterns, scopePatterns, the policy
// forms of isPolicyRule) that is neither a question, posed hypothetically (ruleHypothetical),
// reported from someone else (ruleReported) nor a one-time permission (ruleOneTime).
func isRule(sentence string) bool {
	sentence = strings.ReplaceAll(sentence, "’", "'")
	if isQuestion(sentence) || ruleHypothetical.MatchString(sentence) || isReported(sentence) || ruleOneTime.MatchString(sentence) {
		return false
	}
	for _, p := range rulePatterns {
		if p.MatchString(sentence) {
			return true
		}
	}
	keyed := len(ledger.RecordKeys(sentence)) > 0
	for _, p := range scopePatterns {
		if m := p.FindStringSubmatch(sentence); m != nil {
			object := strings.ToLower(strings.Trim(m[1], "`'\".,;:!?()"))
			if keyed || (object != "" && !scopePronouns[object]) {
				return true
			}
		}
	}
	return isPolicyRule(sentence, keyed)
}

// The policy forms of a standing rule (docs/memory-v2.md, "Conversation facts"): org-policy and
// imperative wording outside the rule verbs. Each counts only when the sentence names a specific
// (policyContent): a key, or a word that is more than a pronoun, a function word or a filler.
var (
	// policyNeverAlways is "never/always <verb> X" standing as an imperative or a policy: at the
	// sentence start (after interjections), after a clause mark, or after a joining or modal
	// word ("and never", "please always", "should never", "to always"). The first group is the
	// verb, the second what follows it to the clause end.
	policyNeverAlways = regexp.MustCompile(`(?i)(?:^(?:(?:ok(?:ay)?|so|also|and|but|oh|please|remember|important|note|reminder)\W+)*|[:;,(]\s*|\b(?:and|or|but|please|should|to)\s+)(?:never|always)\s+([a-z][a-z-]*)\b([^.;:!?]*)`)
	// policyModal is "must / must not / mustn't / shall / shall not <verb> X"; the first group is
	// the verb, the second what follows it.
	policyModal = regexp.MustCompile(`(?i)\b(?:must|shall)(?:\s+not|n't|nt)?\s+(?:(?:always|never|only|not|also|first)\s+)*([a-z][a-z'-]*)\b([^.;:!?]*)`)
	// policyModalEpistemic is "must/shall" read as a guess or the speaker's own aside ("it must
	// be the cache", "you must have deleted it", "I must say"), never a rule.
	policyModalEpistemic = regexp.MustCompile(`(?i)\b(?:i|it|that|this|there|which|what|who)\s+(?:must|shall)\b|\bmust'?ve\b|\bmust\s+have\s+(?:been|done|made|got|gone|seen|run|left|lost|forgot|forgotten|broken|written|[a-z]+ed)\b|\bmust\s+be\s+(?:the|a|an|my|your|our|some|something|because|wrong|right|why|it|that|so|joking|kidding|tired|confused|nice|flaky|busy|down|slow|off|missing|stale|weird|hard|easy|great|crazy)\b`)
	// policyModalGuess is a pronoun's "must be X" with X no participle ("they must be busy"), a
	// guess; "you must be logged in" stays a rule. The group is X.
	policyModalGuess = regexp.MustCompile(`(?i)\b(?:they|he|she|you|we)\s+must\s+be\s+([a-z]+)`)
	// policyAllowed is "is/are (not) allowed", "is/are prohibited / forbidden / not permitted".
	policyAllowed = regexp.MustCompile(`(?i)(?:\b(?:is|are)(?:n't|nt)?|'s|'re)\s+(?:(?:not|never|strictly|only|also|absolutely)\s+)*(?:allowed|permitted|prohibited|forbidden|disallowed|banned)\b`)
	// policyApproval is "requires/require … approval / review / sign-off" and "needs … approval";
	// the first group is the verb, the second the words between, the third the noun.
	policyApproval = regexp.MustCompile(`(?i)\b(requires?|required|needs?|needed)\b([^.;:!?]*?)\b(approval|review|sign-?off|signoff)s?\b`)
	// policyApprovalOwn is the speaker's own need ("I need a review"), a request, never a rule.
	policyApprovalOwn = regexp.MustCompile(`(?i)\b(?:i|we)\s+(?:really\s+|still\s+|just\s+)?(?:need|needs|needed|require|required)\b`)
	// policyMarkers are a stated policy or rule: "policy:", "company/team/org policy is", "rule:",
	// "(we have a) rule that", "the rule is that".
	policyMarkers = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:^|\b(?:company|team|org|organi[sz]ation|security|house|project|repo|our)\s+)(?:policy|rule)\s*:`),
		regexp.MustCompile(`(?i)\b(?:company|team|org|organi[sz]ation|security|house|our)\s+(?:policy|rule)\s+(?:is|says|states|requires)\b`),
		regexp.MustCompile(`(?i)\b(?:we\s+have|there\s+is|there's|i\s+have|our\s+team\s+has)\s+a\s+(?:[a-z-]+\s+)?(?:rule|policy)\s+(?:that|to|against)\b`),
		regexp.MustCompile(`(?i)\b(?:the|our|my)\s+rule\s+is\s+(?:that|to)\b`),
	}
	// policyOnly is "only <verb> X" as an imperative or a policy ("only use", "you may only
	// send"); it counts only when X holds a key. The first group is the verb, the second X.
	policyOnly = regexp.MustCompile(`(?i)(?:^|[:;,]\s*|\b(?:and|please|you|we|to|should|must|may|can)\s+)only\s+([a-z][a-z-]*)\b([^.;:!?]*)`)
	// policyKeyOnly is "X only" with X a key ("deploy to `us-west-2` only"); the first group is
	// X, the second the word after it.
	policyKeyOnly = regexp.MustCompile("(?i)(`[^`\n]+`|[A-Za-z0-9_./~-]+)\\s+only\\b(?:\\s+([a-z]+))?")
	// policyKeyOnlySubject opens a sentence with a subject a report has ("I changed `x` only").
	policyKeyOnlySubject = regexp.MustCompile(`(?i)^(?:i|it|that|this|he|she|they|there)\b`)
	// policyQuoted is a span in double quotes, which a policy form inside does not state.
	policyQuoted = regexp.MustCompile(`"[^"\n]*"|“[^”\n]*”`)
	// policyToken is a word, number or key a policy's specific check reads.
	policyToken = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9_'./@$-]*`)
	// ruleReported is a rule reported from someone else ("he said never …", "according to",
	// "I was told"), and ruleReportedName the same after a name ("Alice said").
	ruleReported     = regexp.MustCompile(`(?i)\b(?:he|she|they|someone|somebody|people|everyone|everybody|(?:my|our|his|her|their|the|a)\s+(?:boss|manager|lead|colleague|coworker|co-worker|teammate|friend|client|customer|reviewer|cto|ceo|admin|mentor|teacher))\s+(?:said|says|told|tells|wrote|writes|mentioned|claims|claimed|insists|insisted|thinks|thought|suggested|suggests)\b|\baccording\s+to\b|\b(?:i\s+was|we\s+were|i've\s+been|we've\s+been)\s+told\b`)
	ruleReportedName = regexp.MustCompile(`\b([A-Z][a-z]+)\s+(?:said|says|told|wrote|mentioned|claims|claimed|thinks|suggested)\b`)
	// ruleOneTime is a permission or instruction for one occasion ("you can push this once",
	// "just this once", "for this task"): it is not a standing rule (decision 23).
	ruleOneTime = regexp.MustCompile(`(?i)\b(?:(?:just\s+)?this\s+once|just\s+once|(?:only\s+)?this\s+(?:one\s+)?time|one[-\s](?:time|off)|for\s+this\s+(?:task|change|commit|pr|one|run|session|ticket|request)|(?:it|that)\s+once)\b`)
)

// ruleReportedNameWords are capitalized words before "said" that name a document, not a person:
// "Policy says never …" states the policy.
var ruleReportedNameWords = map[string]bool{"Policy": true, "Rule": true, "Docs": true, "Documentation": true, "Guidelines": true, "Spec": true, "Readme": true}

// policyVerbStop are the words after never/always/must/only that are no imperative verb: past
// and third-person forms, auxiliaries, determiners and fillers ("never again", "always the case",
// "never had").
var policyVerbStop = map[string]bool{
	"mind": true, "again": true, "ever": true, "before": true, "been": true, "had": true, "has": true,
	"was": true, "were": true, "is": true, "are": true, "did": true, "does": true, "got": true,
	"the": true, "a": true, "an": true, "so": true, "that": true, "this": true, "it": true,
	"really": true, "quite": true, "sure": true, "enough": true, "once": true, "too": true,
	"more": true, "less": true, "heard": true, "seen": true, "said": true, "thought": true,
	"knew": true, "told": true, "went": true, "came": true, "used": true, "true": true,
	"false": true, "if": true, "when": true, "after": true, "for": true, "in": true, "on": true,
	"one": true, "two": true, "just": true, "here": true, "there": true, "then": true, "now": true,
	"very": true, "much": true, "like": true, "until": true, "because": true, "with": true,
	"to": true, "of": true, "at": true, "by": true, "as": true, "some": true, "all": true,
	"what": true, "how": true, "why": true, "who": true, "know": true, "think": true, "guess": true,
	"seem": true, "seems": true, "happen": true, "happens": true, "work": true, "works": true,
}

// policyIngVerbs are the verbs ending in "ing" an imperative may use.
var policyIngVerbs = map[string]bool{"bring": true, "string": true, "ping": true, "sing": true, "ring": true, "swing": true, "sting": true, "fling": true, "cling": true, "wring": true}

// policyFillers are the words that name nothing by themselves: pronouns, determiners,
// prepositions, conjunctions, auxiliaries, fillers and the trigger words.
var policyFillers = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`i me my mine you your yours we us our ours they them their theirs he him his she her hers it its myself yourself ourselves themselves
		this that these those anything everything something nothing anyone anybody someone somebody everyone everybody nobody one ones
		a an the any some all every each no none such other another
		to of in on at for with without from by into onto about over under up down out off away back through after before as than like via per
		and or but nor so if when unless until while because then also
		again ever here there now just really very too even still yet already anymore today first more less much many well please
		be is are was were been being am do does did done have has had can could will would should shall may might must not never always only
		get got go going let lets make sure ok okay fine good right wrong true false ready careful thing things stuff way
		policy rule company team org approval review signoff sign-off allowed permitted prohibited forbidden disallowed banned
		require requires required need needs needed`) {
		policyFillers[w] = true
	}
}

// policyContent reports a word in text that names something: a token (policyToken), trimmed of
// trailing punctuation and lowercased, that is not a filler (policyFillers).
func policyContent(text string) bool {
	for _, t := range policyToken.FindAllString(text, -1) {
		if w := strings.ToLower(strings.TrimRight(t, "'.-")); w != "" && !policyFillers[w] {
			return true
		}
	}
	return false
}

// policyVerb reports a word an imperative may open with: no stop word (policyVerbStop), no past
// ("-ed", but "-eed") or third-person ("-s", but "-ss" and "-us") form, and no "-ing" form but the
// verbs that end so (policyIngVerbs).
func policyVerb(word string) bool {
	w := strings.ToLower(word)
	switch {
	case policyVerbStop[w]:
		return false
	case strings.HasSuffix(w, "ed") && !strings.HasSuffix(w, "eed") && len(w) > 4:
		return false
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us"):
		return false
	case strings.HasSuffix(w, "ing") && !policyIngVerbs[w]:
		return false
	}
	return true
}

// policyOccasion reports an object that is the thing at hand, not a kind of thing: "it" or "them",
// or "this" or "that" followed by no word that names something ("you must delegate this to a
// subagent", but "never use this pattern"). Such an instruction is for the occasion, not a rule.
func policyOccasion(tail string) bool {
	tokens := policyToken.FindAllString(tail, 2)
	if len(tokens) == 0 {
		return false
	}
	switch strings.ToLower(tokens[0]) {
	case "it", "them":
		return true
	case "this", "that":
		return len(tokens) == 1 || policyFillers[strings.ToLower(strings.TrimRight(tokens[1], "'.-"))]
	}
	return false
}

// isModalGuess reports a pronoun's "must be X" with X no participle (policyModalGuess): X ends
// in neither "ed" nor "en".
func isModalGuess(text string) bool {
	m := policyModalGuess.FindStringSubmatch(text)
	if m == nil {
		return false
	}
	w := strings.ToLower(m[1])
	return !strings.HasSuffix(w, "ed") && !strings.HasSuffix(w, "en")
}

// isReported reports a sentence stating someone else's words (ruleReported, ruleReportedName).
func isReported(sentence string) bool {
	if ruleReported.MatchString(sentence) {
		return true
	}
	for _, m := range ruleReportedName.FindAllStringSubmatch(sentence, -1) {
		if !ruleReportedNameWords[m[1]] {
			return true
		}
	}
	return false
}

// isPolicyRule reports a policy form of a standing rule in a user sentence that isRule's
// exclusions passed; keyed is whether the sentence quotes a key. The forms are read outside
// double-quoted spans (policyQuoted), so a quoted "never …" states nothing.
func isPolicyRule(sentence string, keyed bool) bool {
	text := policyQuoted.ReplaceAllString(sentence, " ")
	for _, m := range policyNeverAlways.FindAllStringSubmatch(text, -1) {
		if policyVerb(m[1]) && !policyOccasion(m[2]) && (keyed || policyContent(m[2])) {
			return true
		}
	}
	if !policyModalEpistemic.MatchString(text) && !isModalGuess(text) {
		if loc := policyModal.FindStringSubmatchIndex(text); loc != nil {
			verb := text[loc[2]:loc[3]]
			if (strings.EqualFold(verb, "be") || policyVerb(verb)) && !policyOccasion(text[loc[4]:loc[5]]) && (keyed || policyContent(text[:loc[0]]+" "+text[loc[4]:loc[5]])) {
				return true
			}
		}
	}
	if loc := policyAllowed.FindStringIndex(text); loc != nil && (keyed || policyContent(text[:loc[0]]+" "+text[loc[1]:])) {
		return true
	}
	if !policyApprovalOwn.MatchString(text) {
		if loc := policyApproval.FindStringSubmatchIndex(text); loc != nil {
			verb, noun := strings.ToLower(text[loc[2]:loc[3]]), strings.ToLower(text[loc[6]:loc[7]])
			if (!strings.HasPrefix(verb, "need") || noun == "approval") && (keyed || policyContent(text[:loc[0]]+" "+text[loc[4]:loc[5]]+" "+text[loc[1]:])) {
				return true
			}
		}
	}
	for _, p := range policyMarkers {
		if loc := p.FindStringIndex(text); loc != nil && (keyed || policyContent(text[loc[1]:])) {
			return true
		}
	}
	if !keyed {
		return false
	}
	for _, m := range policyOnly.FindAllStringSubmatch(text, -1) {
		if policyVerb(m[1]) && len(ledger.RecordKeys(m[2])) > 0 {
			return true
		}
	}
	if policyKeyOnlySubject.MatchString(text) {
		return false
	}
	keys := ledger.RecordKeys(sentence)
	for _, m := range policyKeyOnly.FindAllStringSubmatch(text, -1) {
		if slices.Contains(keys, strings.Trim(m[1], "`")) && !policyKeyOnlyNext[strings.ToLower(m[2])] {
			return true
		}
	}
	return false
}

// policyKeyOnlyNext are the words after "X only" that make it a description ("`x` only has two
// functions"), never a rule.
var policyKeyOnlyNext = map[string]bool{
	"is": true, "are": true, "was": true, "were": true, "has": true, "have": true, "had": true,
	"does": true, "did": true, "contains": true, "returns": true, "takes": true, "gets": true,
	"seems": true, "appears": true, "runs": true, "happens": true, "exists": true, "fails": true,
	"breaks": true, "works": true, "matters": true, "shows": true,
}

// isDecision reports a decision in fixed wording (decisionPatterns) that is neither a
// question, posed under a condition (hypotheticalWording) nor one not taken (decisionUnmade).
func isDecision(sentence string) bool {
	sentence = strings.ReplaceAll(sentence, "’", "'")
	if isQuestion(sentence) || hypotheticalWording.MatchString(sentence) || decisionUnmade.MatchString(sentence) {
		return false
	}
	for _, p := range decisionPatterns {
		if p.MatchString(sentence) {
			return true
		}
	}
	return false
}

// isAssistantDecision reports a decision the assistant stated with its reason (isDecision,
// decisionReason): a record even when it gives no specific the user's words do not.
func isAssistantDecision(statement string) bool {
	return isDecision(statement) && decisionReason.MatchString(statement)
}

// keyFile is a key that names a file by itself: a name with an extension ("go.sum").
var keyFile = regexp.MustCompile(`^[A-Za-z0-9_.-]*[A-Za-z0-9_-]\.[A-Za-z][A-Za-z0-9]{0,7}$`)

// userRule sets what code knows of a rule, preference or decision read from the user's own
// turn (contracts/encoding.md): its source is the user's words and its authority the user's,
// and its keys (ledger.RecordKeys) are filed as paths when they name a path or a file and as
// commands otherwise. Level 0's Encode fills only what is left empty. No scope is set: level 0
// widens no scope yet (contracts/encoding.md, "Fields").
func userRule(o *ledger.Observation) {
	o.Source, o.Authority = ledger.SourceUserSaid, ledger.AuthorityUser
	for _, k := range ledger.RecordKeys(o.Content) {
		kind := ledger.KeyCommands
		if strings.Contains(k, "/") || keyFile.MatchString(k) {
			kind = ledger.KeyPaths
		}
		o.Keys.Add(kind, k)
	}
}
