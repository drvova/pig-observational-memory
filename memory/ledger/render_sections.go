package ledger

// The kernel's layout for a summary a model's own summary query selected (docs/memory-
// interface.md, section 8): the query decides which records the tables show and how they are
// grouped; the kernel prices every row in the codec, keeps what fits the room budget, and
// always appends the omitted table, which discloses every record of the memory the summary does
// not show, whether the query left it out or it did not fit. A summary query cannot suppress
// that table: it is computed here, from the records, after the query ran.

// OmittedTableName is the table that discloses the records a summary left out. No summary
// query may produce a table of this name.
const OmittedTableName = omittedTable

// ContentField is the field a summary table must carry for its rows to show their records: a
// row that shows an id without the record's content discloses nothing of it, so the record
// stays in the omitted table.
const ContentField = "content"
