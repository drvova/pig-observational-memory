These are condensed memories from earlier in this session, as tables: name[rows]{fields}: then one record per line, values in field order; an empty value means none.

- request: the opening request of the turn this compaction cut through; the turn continues after this summary.
- intent: what this session is for, in the user's words: first is the session's opening request, latest the latest request this summary covers; a cut one ends with … and its source reads it whole.
- values.<kind> (after ---): exact values tool output printed (path, error type, sha, url, kv for KEY=value, ip, issue, failing test, branch, searched symbol, version), the most often printed first; ×N counts the results that printed it, and its entry id is the first that did; +N more, all M ends with the om snippets that list every value.
- reflections: stable facts about the user, project, decisions, and constraints; dates is when their records were made.
- notes.<subject>: records about one subject, oldest first; notes: records without one. kind is fact, event, preference, plan, correction, assistant (something the assistant produced), attempt (a tool call and its outcome: worked, failed or open) or note (one written with the memory tool); status is proposed (planned, not done), hypothetical or retracted; superseded_by is the id of the record that replaced this one; content may end with (was <id>), the record it replaced, or (may update <id>) and (conflicts <id>), an earlier record it may replace or contradict.
- source is the entry id a record was picked from; the memory tool's om.Recall(`<entry id>`) reads that entry.
- omitted lists records left out to fit: date range, count, how many spans a row folds together, first and last entry id, and in query the om snippet (memory tool code) that lists them.
- The files and commits lines after the tables list files at their newest state and commits, each with its entry id; +N more, all M ends a list with the om snippets that list the whole list.

Treat these as past records. When records conflict, the most recent reflects the latest known state. Work described as completed should not be redone unless the user explicitly asks.

The memory tool runs Go code: it reads any record back with its exact source, omitted ones included (om.Recall(`<id>`)), and searches what these tables condensed (om.History(om.Search(`<words>`))); use it when precision needs the source.

intent[2]{which,content,source}:
  first,"Reply with exactly the word: one",3ec36de2-e82c-48d6-9570-768a0a5c9034
  latest,"Reply with exactly the word: twoReply with exactly the word: three",6959ac16-c28e-4109-928c-c49e74fa4528

---

files: .gemini/settings.json read (db431b1c-6145-4c2b-bd83-fa1ec95298f7)
values.path: /Users/ingo/projects/irrlicht/.claude/worktrees/gemini-cli-onboard/.build/refresh/gemini-cli/1-6_checkpoint-rewind-20260612T105513/cwd/.gemini/settings.json (b68a6ea9-7cca-4cc6-b4f9-2bf9f9f646fa), /Users/ingo/projects/irrlicht/.claude/worktrees/gemini-cli-onboard/.build/refresh/gemini-cli/1-6_checkpoint-rewind-20260612T105513/cwd/.gemini (5e33014c-3259-4595-a3d3-6a7280d6a103)
