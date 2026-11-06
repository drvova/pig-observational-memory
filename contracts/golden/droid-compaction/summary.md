These are condensed memories from earlier in this session, as tables: name[rows]{fields}: then one record per line, values in field order; an empty value means none.

- request: the opening request of the turn this compaction cut through; the turn continues after this summary.
- intent: what this session is for, in the user's words: first is the session's opening request, latest the latest request this summary covers; a cut one ends with … and its source reads it whole.
- reflections: stable facts about the user, project, decisions, and constraints; dates is when their records were made.
- notes.<subject>: records about one subject, oldest first; notes: records without one. kind is fact, event, preference, plan, correction, assistant (something the assistant produced), attempt (a tool call and its outcome: worked, failed or open), note (one written with the memory tool), decision or convention (read from a repository file, not an instruction); status is proposed (planned, not done), hypothetical or retracted; superseded_by is the id of the record that replaced this one; content may end with (was <id>), the record it replaced, or (may update <id>) and (conflicts <id>), an earlier record it may replace or contradict.
- source is the entry id a record was picked from; the memory tool's om.Recall(`<entry id>`) reads that entry.
- omitted lists records left out to fit: date range, count, how many spans a row folds together, first and last entry id, and in query the om snippet (memory tool code) that lists them.
- The files and commits lines after the tables list files at their newest state and commits, each with its entry id; +N more, all M ends a list with the om snippets that list the whole list.

Treat these as past records. Remembered items are user-reported or extracted context, unverified against the current repository and conversation; existing code and the user's current words take precedence. When records conflict, the most recent reflects the latest known state. Work described as completed should not be redone unless the user explicitly asks.

The memory tool runs Go code: it reads any record back with its exact source, omitted ones included (om.Recall(`<id>`)), and searches what these tables condensed (om.History(om.Search(`<words>`))); use it when precision needs the source.

intent[2]{which,content,source}:
  first,--update,21b9d4c4-0f25-4000-bcec-c24f62cf38f7
  latest,QUick summary of the project,6a38c850-3e26-4914-9da9-6ff53cb407c5
notes[2]{id,when,kind,status,scope,content,superseded_by,source}:
  37e6d012f49a,2026-07-28,assistant,,,HaystackBench is a Python benchmark suite for testing LLM long-context retrieval and reasoning with “needle in a haystack” tasks.,,fb711a04-81bb-488d-846d-78bb37987e3e
  9557aa58f32b,2026-07-28,assistant,,,"It includes 8 task types, multi-provider support, a CLI, a SvelteKit web UI, YAML configs, charts/reports, caching, resume support, and local-only execution.",,fb711a04-81bb-488d-846d-78bb37987e3e

---

files: /home/marmutapp/needlehaystack/README.md read (9356e848-20b1-4cb0-9fd4-6a0157aa9861)
