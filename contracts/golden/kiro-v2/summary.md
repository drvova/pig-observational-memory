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

Shown here: 5 of 8 records; 3 left out, listed by 1 span under omitted. Something missing from this summary may still be in memory: before saying it was not mentioned or is unknown, run the om snippet that lists what was left out or om.History(om.Search(`<words>`)).

intent[1]{which,content,source}:
  first,"<redacted: ~73 KB Kiro Crew agent system prompt>",msg00001-0000-4000-8000-000000000000
notes[4]{id,when,kind,status,scope,content,superseded_by,source}:
  9d9f2975340a,,attempt,,,"Output: `Hello World` ✓ Now modifying to \"Hello Universe\": [write \"strReplace\" worked]",,msg00008-0000-4000-8000-000000000000
  cfc293ecdf93,,attempt,,,"Output: `Hello Universe` ✓ Now deleting the file and verifying: [shell \"del hello.py && dir /b\" worked]",,msg00012-0000-4000-8000-000000000000
  63fb9d316670,,assistant,,,"**Summary of what was done:**",,msg00020-0000-4000-8000-000000000000
  e910e4cfdcd6,,assistant,,,Created `hello.py` → printed `Hello World` Updated it → printed `Hello Universe` Deleted it → confirmed absent from directory listing,,msg00020-0000-4000-8000-000000000000
omitted[1]{from,to,count,spans,first_id,last_id,query}:
  2023-11-14,2023-11-14,3,,msg00001-0000-4000-8000-000000000000,msg00004-0000-4000-8000-000000000000,om.Notes(om.From(`msg00001-0000-4000-8000-000000000000`).Through(`msg00004-0000-4000-8000-000000000000`))

---

files: C:\Users\<u>\demo-workspace\antigravity\hello.py edited (msg00008-0000-4000-8000-000000000000)
