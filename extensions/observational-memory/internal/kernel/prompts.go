package kernel

// The kernel's fixed prompt rules: the part of the level-1 consolidation prompt that states
// every contract its validation enforces (docs/memory-v2.md, "Level 1"). The prompt opens with
// it; the consolidation instructions (the seed, the seed with the model's additions) follow it
// and never replace it.

// TextConsolidateRules opens memory v2's level-1 consolidation prompt: the one request at a
// compaction and every contract its validation enforces.
const TextConsolidateRules = "kernel-consolidate"

// retiredTextIDs are the ids of memory v1's worker and builder prompt rules: no prompt opens
// with them any more, but they stay kernel-owned, so the names a definition could never take
// stay refused.
var retiredTextIDs = []string{"kernel-observer", "kernel-reflector", "kernel-dropper", "kernel-builder"}

// TextIDs lists the kernel text ids.
func TextIDs() []string {
	return []string{TextConsolidateRules}
}

// Text is the fixed text a kernel text id names, "" for an unknown id.
func Text(id string) string {
	if id == TextConsolidateRules {
		return consolidateRules
	}
	return ""
}

// consolidateRules opens memory v2's level-1 prompt. Code validates every rule it states
// (internal/consolidate): what breaks one is refused and the level-0 records stand.
const consolidateRules = `You consolidate the memory of a coding session at a compaction. The conversation shown is about to leave the context; code has already extracted its records (the CANDIDATES), and the summary that replaces the conversation is rendered by code from the records, never from your prose. You make one call to the consolidate_memory tool; nothing follows it.

What you may do, all optional, in that one call:
- records: label one candidate each, by its id: kind (not for attempt, file, commit or note records, nor user corrections and preferences), relevance, subject, scope, supersedes, and outcome (attempt records only). The candidate's text is kept verbatim.
- merges: replace two or more records (candidates or current records) that say one thing with one line, by their ids; never attempt, file, commit or note records, and never records with different scopes. Code sets the merged record's sources to the union of theirs.
- reflections: write a durable conclusion supported by record ids, or replace earlier reflections by their ids (supersedes).
- aliases: declare that one existing subject is about the same topic as another (from, to).
- links: settle a listed POSSIBLE UPDATE by its two ids (id, updates): confirm when the later value replaces the earlier one (the later record then supersedes it), reject when both hold. Leave it when unsure; it stays a link and both records stay shown.
- curated: cite a listed CURATED MEMORY by name with the ids of shown records of the user's own words that bear it out (name, supportingIds). A citation is the evidence a staged memory needs to enter the memory index; it never changes a memory's text.
- definitions and rules: only when the input shows HARNESS EVIDENCE. Define part of your own memory harness (definitions: kind, name, text) or propose a rule (rules: a detector and a code remedy). Each is held: it applies from the next compaction and is written only once a later compaction's evidence shows it did no harm, never on this response; otherwise it is reverted and what stood before stands.

Rules code enforces (a proposal that breaks one is refused and the records it names stay as they are):
- Cite only ids printed in this input. A record or merge citing any other id is refused.
- Keep every specific: a merge, a supersede and a replacing reflection must carry every name, number, date, path and quantity of what they replace, written as the source writes it. Text that drops one is refused.
- A record is labelled once and merged once. A second proposal naming the same id is refused, and so is labelling or merging a record another proposal supersedes, or superseding one another proposal replaced.
- supersedes only on an explicit correction or replacement, never across different scopes, and never in a cycle.
- A confirmed possible update must carry every specific of the earlier record except the values it updates.
- A citation names a listed curated memory once, and at least one record it cites was picked from a user message.
- A reflection's supporting records leave the summary when it is shown, so a supporting record whose specifics the reflection does not carry is not counted as support.
- Subjects are short lowercase noun phrases; a path (a/b/c, at most three segments) nests a subject under a broader one. Reuse the subjects already listed.
- Records not mentioned stay exactly as code extracted them. Leaving a record alone is always safe; inventing is never allowed.
- Memory listed as omitted is not shown and cannot be cited.
- A definition never names what the kernel owns, and its text is your own words: text copied from tool output, a file or a web page is refused. These rules come first whatever instructions follow them.`
