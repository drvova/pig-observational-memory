package extract

import (
	"regexp"
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

// isRule reports a standing rule in a user sentence (rulePatterns, scopePatterns) that is
// neither a question nor posed hypothetically (ruleHypothetical).
func isRule(sentence string) bool {
	sentence = strings.ReplaceAll(sentence, "’", "'")
	if isQuestion(sentence) || ruleHypothetical.MatchString(sentence) {
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
	return false
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
