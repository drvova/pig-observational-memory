package harness

import (
	"fmt"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
)

// The states of a tool the agent defined (docs/memory-interface.md, section 6).
const (
	// StateProbation is a new definition: registered and active, not yet proven.
	StateProbation = "probation"
	// StateEstablished is a definition that returned results without error twice.
	StateEstablished = "established"
	// StateRetired is a definition that errored more than it succeeded, or that feedback marked
	// harmed: it stays stored but is deactivated and refuses calls until it is redefined.
	StateRetired = "retired"
)

// EstablishAfter is how many calls that return results without error establish a definition.
const EstablishAfter = 2

// Standing is a definition's probation state and the counts that decided it.
type Standing struct {
	State string
	// Reason says why it is in this state.
	Reason string
	// Calls, Results, Empty and Errors count its calls since the current definition was saved.
	Calls, Results, Empty, Errors int
}

// Probation is a definition's standing from its usage ledger events (curated.Store.UsageEvents
// of curated.DefinitionUsageKey), in the order they were written. Only events after the latest
// save count: a redefined name starts over. The rules, applied event by event:
//
//   - feedback marking it harmed retires it at once;
//   - on probation it is retired when its calls since the definition meet the minimum-evidence
//     rule (MinEvidence) with more errors than calls without error, and established once
//     EstablishAfter calls returned results without error;
//   - established, it is retired when its calls since it was established meet the same rule.
//
// Retirement is final for that definition.
func Probation(events []curated.UsageEvent) Standing {
	start := 0
	for i, e := range events {
		if e.Event == curated.EventSave {
			start = i + 1
		}
	}
	s := Standing{State: StateProbation, Reason: "new definition"}
	var since tally
	for _, e := range events[start:] {
		if s.State == StateRetired {
			break
		}
		switch e.Event {
		case curated.EventHarmed:
			s.State, s.Reason = StateRetired, "feedback marked it harmed"
			continue
		case curated.EventCallOK:
			s.Calls++
			s.Results++
			since.keep++
		case curated.EventCallEmpty:
			s.Calls++
			s.Empty++
			since.keep++
		case curated.EventCallError:
			s.Calls++
			s.Errors++
			since.against++
		default:
			continue
		}
		if since.due() {
			s.State, s.Reason = StateRetired, fmt.Sprintf("%d errors vs %d calls without error (needs at least %d and a majority)", since.against, since.keep, MinEvidence)
			continue
		}
		if s.State == StateProbation && e.Event == curated.EventCallOK && s.Results >= EstablishAfter {
			s.State, s.Reason = StateEstablished, fmt.Sprintf("returned results without error %d times", s.Results)
			since = tally{}
		}
	}
	return s
}
