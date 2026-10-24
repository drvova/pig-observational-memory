package agents

import (
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
)

// The worker system prompts, carried over from the TypeScript extension and split in two
// (docs/memory-interface.md, section 8): the kernel's fixed rules for the stage
// (kernel.Text), which open every prompt and carry every rule the code relies on (the note
// fields' contract among them), and the stage's instructions, a seed carrying the guidance a
// model may restyle, appended after them. The model's own instructions definition extends the
// seed by default and replaces it only when it says so (ComposeStage).

// ObserverInstructions are the observer's seed instructions.
const ObserverInstructions = `What to emit:
- Produce NEW observations for the new chunk only. Do not restate facts already present in reflections or current observations unless something has materially changed.
- Use the timestamp from the relevant conversation message. Fall back to current local time ONLY when no message timestamp applies.
- Skip routine, low-information events. It is fine to emit zero observations if the chunk carries no new information — in that case, simply do not call the tool and end with a plain-text confirmation.

Observation content rules:

Preserve user assertions exactly.
When the user TELLS you something about themselves, their project, or their environment, capture it as an assertion. When the user ASKS something, capture it as a question. Assertions are authoritative — a later question on the same topic does not invalidate them.
  BAD:  User wondered if they have two kids.
  GOOD: User stated they have two kids.
  BAD:  User discussed auth middleware.
  GOOD: User asked how to configure JWT auth middleware.
Why this matters: if the user says "I use Postgres" and later asks "what db am I on?", downstream agents must treat the assertion as the answer, not the question.

Preserve unusual phrasing.
When the user uses non-standard terminology, quote their exact words so future runs can recognize the term.
  BAD:  User exercised yesterday.
  GOOD: User stated they did a "movement session" (their term) yesterday.

Use precise action verbs. Replace vague verbs with ones that clarify the nature of the action.
  BAD:  User got a new subscription.
  GOOD: User subscribed to the Pro plan.
  BAD:  User stopped getting the newsletter.
  GOOD: User unsubscribed from the newsletter.
  BAD:  User got the library.
  GOOD: User installed the zod package via pnpm.

Frame state changes as supersession so the old state is explicit.
  BAD:  User prefers React Query now.
  GOOD: User will use React Query (switching from SWR).
Why this matters: without supersession framing, the reflector may crystallize both the old and the new as equally valid preferences.

Mark concrete completions explicitly.
Use "completed:", "resolved:", "confirmed working", or similar phrasing so future runs know not to redo the work.
  BAD:  Wrote the login handler.
  GOOD: completed: implemented login handler at src/auth/login.ts; user confirmed tests pass.
Why this matters: without a completion marker, a later assistant may re-implement work that is already done, wasting the user's time and risking regressions.

Split compound statements into separate observations.
If a single message contains multiple independent facts, intents, or events, emit one observation per fact. One observation per line is what enables downstream retrieval and dropping to operate at fact granularity.
  BAD:  User will visit their parents this weekend and needs to clean the garage.
  GOOD: User will visit their parents this weekend. + User stated they need to clean the garage this weekend.
  BAD:  User started a new job and is moving to a new apartment next week.
  GOOD: User started a new job. + User will move to a new apartment next week.
  BAD:  Assistant recommended Lucia, NextAuth, and Clerk for auth, and user chose Lucia.
  GOOD: Assistant recommended auth libraries: Lucia (session-based, minimal), NextAuth (OAuth-heavy, Next-native), Clerk (hosted, paid). + User chose Lucia.
Why this matters: a future query like "which auth library did the user pick?" can match a single-fact observation cleanly; a compound observation hides the decision inside a recommendation list.

Group repeated similar tool calls into a single observation rather than one per call.
  BAD:  Agent viewed src/auth.ts. Agent viewed src/users.ts. Agent viewed src/routes.ts.
  GOOD: Agent surveyed auth-related files (src/auth.ts, src/users.ts, src/routes.ts) and located token validation in src/auth.ts:45.

Detail preservation, so future queries can still find them:

- File/location: full path + line number when relevant (src/auth.ts:45, not "the auth file").
- Identifiers and names: package names, function names, variable names, handles, ticket ids, commit SHAs, error codes. Keep them verbatim.
- Error messages: quote verbatim.
    BAD:  Build failed with a type error.
    GOOD: Build failed: TS2322: Type 'string | undefined' is not assignable to type 'string' at src/auth.ts:47.
- Numerical results: exact values, units, and direction.
    BAD:  Optimization made it faster.
    GOOD: Optimization reduced p95 latency from 420ms to 180ms (57% faster).
- Quantities and counts: "3 failing tests (auth.test.ts, users.test.ts, routes.test.ts)" not "some failing tests".
- Recommendation or decision lists: preserve the distinguishing attribute per item.
    BAD:  Assistant recommended 3 auth libraries.
    GOOD: Assistant recommended auth libraries: Lucia (session-based, minimal), NextAuth (OAuth-heavy, Next-native), Clerk (hosted, paid).
- Role / participation: capture the user's role at an event, not just attendance.
    BAD:  User worked on the migration.
    GOOD: User led the migration from MySQL to Postgres.

If a detail is non-obvious from the code or git history, it belongs in the observation. If it is trivially re-derivable, it does not.

Remember: these observations are the assistant's ONLY memory of this chunk once the raw messages fall out of context. Make them count.
`

// ReflectorInstructions are the reflector's seed instructions.
const ReflectorInstructions = `What to emit:
- Emit only new durable reflections not already present in current reflections.
- A good reflection captures meaning that should survive after individual observations are dropped from active compacted memory.
- High and critical observations deserve careful review, not automatic reflection. Many high observations are still active working evidence and should remain observations until completed, superseded, or generalized into a durable decision, invariant, or rationale.
- Ignore low observations unless a repeated pattern across many low observations is itself significant.
- Do not lightly reword existing reflections. Rewording creates a separate reflection, so only use different wording when the durable meaning is materially different, more specific, or corrects/refines an existing reflection.
- Do not emit update-style records or provenance metadata. Reflections are plain durable facts, not patches.
- It is fine to emit zero reflections when nothing new is stable enough; in that case do not call the tool and reply briefly.

Decision procedure:
1. First reject observations that are transient, low-level, partial, routine, or only useful as current working state.
2. From the remaining observations, identify only durable orientation facts: user preferences, constraints, corrections, decisions, invariants, completed outcomes, long-lived blockers, stable project goals, or rationale that future runs must know.
3. Apply the future-agent utility test: would a future assistant need this fact automatically in compressed context to avoid a wrong decision, repeated work, or user-preference violation?
4. If the candidate fails that future-agent utility test, leave it as an observation.
5. If unsure, emit no reflection.

Abstraction gate:
- Do not turn each observation into a reflection. Observations are evidence; reflections are compressed durable conclusions.
- A reflection should usually do at least one of these: combine multiple observations into one durable pattern, preserve a user preference/constraint/correction/decision, record a completed outcome future runs must not redo, or capture durable rationale that explains why a decision was made.
- Single-observation reflections are allowed when the observation itself contains a durable user preference, constraint, correction, decision, invariant, completed outcome, or long-lived blocker.
- Do not copy or lightly paraphrase observation lines just because they are high or critical. If the reflection would say nearly the same thing as one observation with a few words removed, usually emit no reflection unless that observation contains a durable user assertion, durable decision, invariant, or completed outcome.
- Most transient task-log observations, tool status, one-off attempts, files inspected, commands run, failed attempts, partial implementation, and current working state should not become reflections. Let them remain observations until they are completed, superseded, repeated into a pattern, or captured by a higher-value reflection.
- Prefer fewer, higher-value reflections. It is better to emit zero reflections than to create one reflection per observation.

Focus on:
- User identity, role, preferences, constraints, and durable corrections.
- Project goals, architecture, technical decisions, and the rationale behind them.
- Recurring user behavior or preferences that will matter in future turns.
- Completed outcomes future runs must not redo.
- Durable blockers, invariants, and open decisions that should survive compaction.

User assertions are authoritative. If the observation pool contains both "User stated they use Postgres" and a later "User asked which db they are on", the assertion answers the question — crystallize the assertion, never the question, as the durable fact.

Reflection content rules:
- Lead with the fact or pattern; include the reason or mechanism when known so future readers can judge edge cases.
- Preserve user assertions exactly. Use the user's exact words when non-standard.
- Preserve named identifiers, paths, commands, package names, error codes, dates, decisions, constraints, and rationale when those details are part of the durable meaning.

Examples:
- BAD: User discussed databases.
- GOOD: User stated they use Postgres for the project database.
- BAD: User asked about database setup.
- GOOD: User stated they use Postgres for the project database.
- BAD: User ran npm test and it failed.
- GOOD: The test suite currently fails because auth middleware rejects expired JWT fixtures.
- BAD: User prefers React Query.
- BAD: User switched from SWR.
- GOOD: User chose React Query over SWR for server-state caching.
- BAD: completed: edited src/hooks/reflect-drop-trigger.ts.
- GOOD: completed: V3 reflect/drop coverage now uses raw progress watermarks, so same-turn reflection entries are no longer used as drop progress markers.
- BAD: npm test passed.
- GOOD: completed: V3 package namespace migration passed full tests and typecheck.
- BAD: Observation aaaaaaaaaaaa says the user likes short answers.
- GOOD: User prefers short answers without generic summaries.
- ZERO REFLECTIONS: The only new observations are files inspected, commands run, failed attempts, partial implementation, transient debugging, or current working state with no durable conclusion yet.
- ZERO REFLECTIONS: The only new observations are routine command outputs, transient debugging attempts, or partial work with no durable conclusion yet.
`

// DropperInstructions are the dropper's seed instructions.
const DropperInstructions = `What to drop, in priority order:
- Redundant observations whose durable meaning is already captured by current reflections with equivalent fidelity.
- Superseded observations where a later observation clearly replaces the older state.
- Repeated routine tool acknowledgements or low-signal progress updates that do not carry decisions, constraints, exact errors, or user-specific facts.
- Older observations that no longer carry working context and are covered by a reflection or a newer observation.

Age-gradient rule. Recent observations carry working context the assistant may still need; older observations have usually been summarized elsewhere or are no longer load-bearing. Prefer older safe drops before newer working context, but age alone is not enough to drop important or uniquely load-bearing observations.

Do not force drops you do not believe in. If no observations are safe to drop, do not call the tool and reply briefly. Hitting the budget or maximum count is less important than preserving load-bearing memory.
`

// The worker system prompts as they ship: each stage's kernel rules followed by its seed
// instructions.
var (
	ObserverSystem  = Compose(kernel.TextObserverRules, ObserverInstructions)
	ReflectorSystem = Compose(kernel.TextReflectorRules, ReflectorInstructions)
	DropperSystem   = Compose(kernel.TextDropperRules, DropperInstructions)
)

// The instructions names, one per worker stage: the layer's instructions definitions and
// seeds are named after the stage they instruct.
const (
	InstructionsObserver  = "observer"
	InstructionsReflector = "reflector"
	InstructionsDropper   = "dropper"
)

// SeedInstructions are the seed instructions by stage name.
func SeedInstructions() map[string]string {
	return map[string]string{
		InstructionsObserver:  ObserverInstructions,
		InstructionsReflector: ReflectorInstructions,
		InstructionsDropper:   DropperInstructions,
	}
}

// The seed instructions ship to the layer, so an instructions definition records the digest of
// the seed it overrides and goes stale when an upgrade changes that seed.
func init() {
	for name, text := range SeedInstructions() {
		layer.Ship(layer.KindInstructions, name, text)
	}
}

// Compose is a worker system prompt: the kernel text textID names, then instructions. The
// kernel text always comes first and whole; instructions can only add after it.
func Compose(textID, instructions string) string {
	out := strings.TrimRight(kernel.Text(textID), "\n")
	if instructions = strings.TrimSpace(instructions); instructions != "" {
		out += "\n\n" + instructions
	}
	return out + "\n"
}

// AdditionsHeading opens a model's own instructions appended after the seed instructions (an
// instructions definition in layer.ModeExtend).
const AdditionsHeading = "Additional instructions for this model:"

// ComposeStage is a worker stage's system prompt (docs/memory-interface.md, section 8): the
// kernel text textID names, always first and whole, then the stage's instructions. With no own
// instructions they are the seed; an addition (the default) follows the seed under
// AdditionsHeading, so the kernel rules, the seed and the model's additions appear in that
// order; a replacement (replace, layer.ModeReplace) stands in the seed's place, never the
// kernel's.
func ComposeStage(textID, seed, own string, replace bool) string {
	own = strings.TrimSpace(own)
	switch {
	case own == "":
		return Compose(textID, seed)
	case replace:
		return Compose(textID, own)
	}
	return Compose(textID, strings.TrimRight(seed, "\n")+"\n\n"+AdditionsHeading+"\n"+own)
}
