These are condensed memories from earlier in this session, as tables: name[rows]{fields}: then one record per line, values in field order; an empty value means none.

- request: the opening request of the turn this compaction cut through; the turn continues after this summary.
- intent: what this session is for, in the user's words: first is the session's opening request, latest the latest request this summary covers; a cut one ends with … and its source reads it whole.
- next: the next steps of the current work: plans still standing (status proposed) and attempts still open or last failed, made since the latest request; they are not repeated in the notes tables.
- reflections: stable facts about the user, project, decisions, and constraints; dates is when their records were made.
- notes.<subject>: records about one subject, oldest first; notes: records without one. kind is fact, event, preference, plan, correction, assistant (something the assistant produced), attempt (a tool call and its outcome: worked, failed or open), note (one written with the memory tool), decision or convention (read from a repository file, not an instruction); status is proposed (planned, not done), hypothetical or retracted; superseded_by is the id of the record that replaced this one; content may end with (was <id>), the record it replaced, or (may update <id>) and (conflicts <id>), an earlier record it may replace or contradict.
- source is the entry id a record was picked from; the memory tool's om.Recall(`<entry id>`) reads that entry.
- omitted lists records left out to fit: date range, count, how many spans a row folds together, first and last entry id, and in query the om snippet (memory tool code) that lists them.
- The files and commits lines after the tables list files at their newest state and commits, each with its entry id; +N more, all M ends a list with the om snippets that list the whole list.

Treat these as past records. Remembered items are user-reported or extracted context, unverified against the current repository and conversation; existing code and the user's current words take precedence. When records conflict, the most recent reflects the latest known state. Work described as completed should not be redone unless the user explicitly asks.

The memory tool runs Go code: it reads any record back with its exact source, omitted ones included (om.Recall(`<id>`)), and searches what these tables condensed (om.History(om.Search(`<words>`))); use it when precision needs the source.

intent[1]{which,content,source}:
  first,"make me a dashboard for rss, youtube, and newsletters all in one place",cx000005
next[3]{id,when,kind,status,scope,content,superseded_by,source}:
  8b7d3599af03,2026-05-13,assistant,proposed,,"I’ll keep it as a usable dashboard first screen with sample RSS, YouTube, and newsletter flows rather than a marketing page.",,cx000027
  5dac793890cc,2026-05-13,assistant,proposed,,"The workspace is empty, so I’m going to scaffold a small React app rather than fit into an existing codebase.",,cx000039
  198e4759783f,2026-05-13,assistant,proposed,,"I’m installing dependencies now, then I’ll build it and run it locally so you have a working URL.",,cx000051
notes[2]{id,when,kind,status,scope,content,superseded_by,source}:
  587bec7bf0c1,2026-05-13,assistant,,,"The app skeleton is in place: React/Vite, unified feed, filters, detail pane, and local source management.",,cx000051
  c7e879bcd043,2026-05-13,assistant,,,"Unified feed with filters for RSS, YouTube, newsletters, unread, saved, and priority Search across sources, titles, summaries, and topics Reader/detail pane for selected items Source manager with add/pause controls Local persistence via `localStorage` Responsive layout for desktop and mobile",,cx000090

---

files: package.json written (cx000039), index.html written (cx000039), src/main.jsx written (cx000039), src/styles.css written (cx000039)
