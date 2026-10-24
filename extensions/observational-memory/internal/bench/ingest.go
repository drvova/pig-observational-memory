package bench

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/hooks"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
)

// TurnSpacing separates consecutive turns of one session, as Mastra's
// LongMemEval runner spaces them.
const TurnSpacing = 5 * time.Second

// WorkerConfig is the extension's default configuration with the bench model
// as the memory model and notifications off.
func WorkerConfig(spec ModelSpec, thinking string) config.Config {
	cfg := config.Defaults
	cfg.Model = &config.ConfiguredModel{Provider: Provider, ID: spec.ID, Thinking: config.ThinkingLevel(thinking)}
	cfg.ShowWorkerNotifications = false
	return cfg
}

// Checkpoint is ingestion progress, saved after every session.
type Checkpoint struct {
	SessionsDone int              `json:"sessionsDone"`
	Entries      []map[string]any `json:"entries"`
	Errors       []string         `json:"errors,omitempty"`
}

// IngestOptions configure one question's ingestion.
type IngestOptions struct {
	Config config.Config
	Spec   ModelSpec
	// Flush observes the unobserved tail after the last session, below
	// observeAfterTokens too. Without it the tail stays raw, as Pi keeps
	// unobserved source in context across a compaction.
	Flush bool
	// Resume continues from a saved checkpoint.
	Resume *Checkpoint
	// OnSession runs after each session with the progress to save.
	OnSession func(Checkpoint) error
}

// IngestResult is a question's ingested branch.
type IngestResult struct {
	Entries []session.Entry `json:"-"`
	// Errors are the distinct worker-stage errors the runtime recorded.
	Errors []string `json:"errors,omitempty"`
	// PipelineRuns counts consolidation runs.
	PipelineRuns int `json:"pipelineRuns"`
}

// ingestMu serializes ingestion within a process: the observer prompt reads
// the package clock serialize.Now, which ingestion points at the replayed
// conversation's time. The command runs one question per process.
var ingestMu sync.Mutex

// Ingest replays a question's haystack sessions oldest first through the
// extension's consolidation pipeline: after every assistant reply (a Pi
// turn_end) it runs RunConsolidationPipeline, whose stages decide themselves
// whether the observer, reflector and dropper are due.
func Ingest(ctx context.Context, q Question, models host.Models, opts IngestOptions) (IngestResult, error) {
	ingestMu.Lock()
	defer ingestMu.Unlock()
	sessions, err := q.ChronologicalSessions()
	if err != nil {
		return IngestResult{}, err
	}
	var resumeEntries []session.Entry
	start := 0
	errs := &errorLog{}
	if opts.Resume != nil {
		resumeEntries = session.FromMaps(opts.Resume.Entries)
		start = min(opts.Resume.SessionsDone, len(sessions))
		errs.items = append(errs.items, opts.Resume.Errors...)
	}
	h := NewHost(q.ID, models, opts.Spec.HostModel(), resumeEntries)
	rt := runtime.New()
	rt.SetConfig(opts.Config)

	clock := time.Time{}
	restoreNow, restoreLoc := serialize.Now, serialize.Location
	serialize.Now = func() time.Time { return clock }
	serialize.Location = time.UTC
	defer func() { serialize.Now, serialize.Location = restoreNow, restoreLoc }()
	if n := len(resumeEntries); n > 0 {
		if t, ok := serialize.ParseTimestamp(resumeEntries[n-1].Timestamp); ok {
			clock = t
		}
	}

	runs := 0
	run := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		runs++
		if err := hooks.RunConsolidationPipeline(ctx, rt, h); err != nil {
			return err
		}
		errs.note(rt.Snapshot())
		return ctx.Err()
	}

	for i := start; i < len(sessions); i++ {
		s := sessions[i]
		appended := 0
		for t, turn := range s.Turns {
			if strings.TrimSpace(turn.Content) == "" {
				continue
			}
			role := "user"
			if turn.Role == "assistant" {
				role = "assistant"
			}
			clock = s.Date.Add(time.Duration(t) * TurnSpacing)
			h.AppendMessage(role, turn.Content, clock.UnixMilli())
			appended++
			if role == "assistant" {
				if err := run(); err != nil {
					return IngestResult{}, err
				}
			}
		}
		// A session that ends on a user turn still ends a Pi turn.
		if appended > 0 && s.Turns[len(s.Turns)-1].Role != "assistant" {
			if err := run(); err != nil {
				return IngestResult{}, err
			}
		}
		if opts.OnSession != nil {
			if err := opts.OnSession(Checkpoint{SessionsDone: i + 1, Entries: entryMaps(h.Entries()), Errors: errs.items}); err != nil {
				return IngestResult{}, err
			}
		}
	}

	// Drain: one run observes one capped chunk, so a backlog over the
	// threshold may need more runs. Stop as soon as a run makes no progress.
	drainCfg := opts.Config
	if opts.Flush {
		drainCfg.ObserveAfterTokens = 1
		rt.SetConfig(drainCfg)
	}
	for range 50 {
		backlog := hooks.ObserverBacklog(h.Entries())
		if backlog == 0 || backlog < drainCfg.ObserveAfterTokens {
			break
		}
		if err := run(); err != nil {
			return IngestResult{}, err
		}
		if hooks.ObserverBacklog(h.Entries()) >= backlog {
			break
		}
	}
	return IngestResult{Entries: h.Entries(), Errors: errs.items, PipelineRuns: runs}, nil
}

func entryMaps(entries []session.Entry) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ToMap())
	}
	return out
}

// errorLog keeps each distinct stage error once, in order.
type errorLog struct {
	items []string
	last  runtime.State
}

func (l *errorLog) note(state runtime.State) {
	for _, pair := range [][2]string{
		{"observer", state.LastObserverError},
		{"reflector", state.LastReflectorError},
		{"dropper", state.LastDropperError},
	} {
		if pair[1] == "" {
			continue
		}
		item := pair[0] + ": " + pair[1]
		seen := false
		for _, existing := range l.items {
			if existing == item {
				seen = true
				break
			}
		}
		if !seen {
			l.items = append(l.items, item)
		}
	}
}

// Memory is what the actor sees from an ingested branch.
type Memory struct {
	// Summary is the compaction summary the extension renders for the
	// folded ledger at the branch tip, within compactionSummaryMaxTokens.
	Summary string `json:"summary"`
	// Tail is the unobserved source after the observation frontier,
	// serialized with timestamps; Pi keeps it in context raw.
	Tail                string `json:"tail"`
	Observations        int    `json:"observations"`
	ActiveObservations  int    `json:"activeObservations"`
	Reflections         int    `json:"reflections"`
	OmittedObservations int    `json:"omittedObservations"`
	OmittedReflections  int    `json:"omittedReflections"`
	SummaryTokens       int    `json:"summaryTokens"`
	TailTokens          int    `json:"tailTokens"`
	SourceTokens        int    `json:"sourceTokens"`
}

// RenderMemory renders the branch's memory the way the compaction hook does
// for a full fold at the tip: reflections and active observations, budgeted.
func RenderMemory(entries []session.Entry, cfg config.Config, contextWindow int) Memory {
	full := ledger.FullProjection(entries, "")
	folded := ledger.FoldLedger(entries, "")
	rendered := ledger.RenderSummaryWithOptions(full.Reflections, full.Observations, config.ResolveCompactionSummaryMaxTokens(cfg, contextWindow), ledger.RenderOptions{ReflectionDates: ledger.ReflectionDateRanges(folded)})
	frontier := ledger.LatestCoverageIndex(entries, ledger.OMObservationsRecorded)
	tail := []session.Entry{}
	for i := frontier + 1; i < len(entries); i++ {
		if ledger.IsSourceEntry(entries[i]) {
			tail = append(tail, entries[i])
		}
	}
	tailText := serialize.SerializeBranchEntries(tail)
	return Memory{
		Summary:             rendered.Text,
		Tail:                tailText,
		Observations:        len(folded.Observations),
		ActiveObservations:  len(full.Observations),
		Reflections:         len(full.Reflections),
		OmittedObservations: rendered.OmittedObservations,
		OmittedReflections:  rendered.OmittedReflections,
		SummaryTokens:       EstimateTokens(rendered.Text),
		TailTokens:          EstimateTokens(tailText),
		SourceTokens:        ledger.RawTokensAfterIndexWhere(entries, -1, nil),
	}
}

// EstimateTokens is the extension's chars/4 estimate.
func EstimateTokens(text string) int { return (len([]rune(text)) + 3) / 4 }

// String is a short memory report.
func (m Memory) String() string {
	return fmt.Sprintf("%d active observations (%d recorded), %d reflections, summary ~%d tokens, tail ~%d tokens, source ~%d tokens",
		m.ActiveObservations, m.Observations, m.Reflections, m.SummaryTokens, m.TailTokens, m.SourceTokens)
}
