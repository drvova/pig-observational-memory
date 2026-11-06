These are condensed memories from earlier in this session, as tables: name[rows]{fields}: then one record per line, values in field order; an empty value means none.

- request: the opening request of the turn this compaction cut through; the turn continues after this summary.
- intent: what this session is for, in the user's words: first is the session's opening request, latest the latest request this summary covers; a cut one ends with … and its source reads it whole.
- reflections: stable facts about the user, project, decisions, and constraints; dates is when their records were made.
- notes.<subject>: records about one subject, oldest first; notes: records without one. kind is fact, event, preference, plan, correction, assistant (something the assistant produced), attempt (a tool call and its outcome: worked, failed or open) or note (one written with the memory tool); status is proposed (planned, not done), hypothetical or retracted; superseded_by is the id of the record that replaced this one; content may end with (was <id>), the record it replaced, or (may update <id>) and (conflicts <id>), an earlier record it may replace or contradict.
- source is the entry id a record was picked from; the memory tool's om.Recall(`<entry id>`) reads that entry.
- omitted lists records left out to fit: date range, count, how many spans a row folds together, first and last entry id, and in query the om snippet (memory tool code) that lists them.
- The files and commits lines after the tables list files at their newest state and commits, each with its entry id; +N more, all M ends a list with the om snippets that list the whole list.

Treat these as past records. Remembered items are user-reported or extracted context, unverified against the current repository and conversation; existing code and the user's current words take precedence. When records conflict, the most recent reflects the latest known state. Work described as completed should not be redone unless the user explicitly asks.

The memory tool runs Go code: it reads any record back with its exact source, omitted ones included (om.Recall(`<id>`)), and searches what these tables condensed (om.History(om.Search(`<words>`))); use it when precision needs the source.

Shown here: 0 of 6 records; 6 left out, listed by 1 span under omitted. Something missing from this summary may still be in memory: before saying it was not mentioned or is unknown, run the om snippet that lists what was left out or om.History(om.Search(`<words>`)).

intent[1]{which,content,source}:
  latest,"You are @spike-a, an agent in a shared conversation with a person, the session's main assistant (Qwen) and possibly other agents. Below are the messages posted since your last turn, oldest first. Messages marked addressed_to_you=\"true\" are the ones that called on you. Reply to the latest message addressed to you. Your reply is posted into the conversatio…",613473b3-69ab-425d-842e-7592e67026e2
omitted[1]{from,to,count,spans,first_id,last_id,query}:
  2026-10-12,2026-10-12,6,,30cee165-cf3f-4e13-aaec-36c26a2ab794,249dc934-03d8-4e40-afa2-f6359dd15ba5,om.Notes(om.From(`30cee165-cf3f-4e13-aaec-36c26a2ab794`).Through(`249dc934-03d8-4e40-afa2-f6359dd15ba5`))
