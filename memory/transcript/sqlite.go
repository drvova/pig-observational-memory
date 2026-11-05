package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// Some harnesses keep their sessions in SQLite. om reads those without linking an SQLite driver
// (the engine module is also PiG's): through the sqlite3 command (-readonly -json) when it is on
// PATH, else through Python's built-in sqlite3 module.

// SQLiteCommands are the programs tried, in order; a test points them at what it has.
var SQLiteCommands = []string{"sqlite3", "python3"}

const sqlitePython = `import json, sqlite3, sys, urllib.parse
c = sqlite3.connect("file:" + urllib.parse.quote(sys.argv[1]) + "?mode=ro", uri=True)
c.row_factory = sqlite3.Row
print(json.dumps([dict(r) for r in c.execute(sys.argv[2])]))`

// ErrNoSQLite says neither sqlite3 nor python3 is on PATH.
var ErrNoSQLite = errors.New("om reads this harness's SQLite sessions with the sqlite3 command or python3; install either")

// QuerySQLite runs a read-only query on the database at path and returns its rows; a missing
// database has none.
func QuerySQLite(path, query string) ([]map[string]any, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	for _, name := range SQLiteCommands {
		bin, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		var cmd *exec.Cmd
		if strings.Contains(name, "python") {
			cmd = exec.Command(bin, "-I", "-c", sqlitePython, path, query)
		} else {
			cmd = exec.Command(bin, "-readonly", "-json", path, query)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("%s %s: %v: %s", name, path, err, strings.TrimSpace(stderr.String()))
		}
		var rows []map[string]any
		if len(bytes.TrimSpace(out)) == 0 {
			return nil, nil
		}
		if err := json.Unmarshal(out, &rows); err != nil {
			return nil, fmt.Errorf("%s %s: %w", name, path, err)
		}
		return rows, nil
	}
	return nil, ErrNoSQLite
}

// SQLQuote is s as an SQL string literal.
func SQLQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// DB is a transcript read from one session's rows of a harness's SQLite database.
type DB struct {
	Path, Session string
	Kind          DBFormat
}

// DBFormat is a SQLite transcript format: the query for one session's rows, in order, and the
// reader of those rows.
type DBFormat struct {
	Query func(session string) string
	Read  Format
}

// Entries reads the session's rows.
func (d DB) Entries() ([]session.Entry, error) {
	rows, err := QuerySQLite(d.Path, d.Kind.Query(d.Session))
	if err != nil {
		return nil, err
	}
	return d.Kind.Read(rows), nil
}

// DBFormats are the SQLite transcript formats by name.
var DBFormats = map[string]DBFormat{
	"crush": {Query: func(s string) string {
		return "SELECT id, role, parts, created_at, is_summary_message FROM messages WHERE session_id = " + SQLQuote(s) + " ORDER BY created_at, rowid"
	}, Read: CrushRows},
	"goose": {Query: func(s string) string {
		return "SELECT message_id, role, content_json, created_timestamp, metadata_json FROM messages WHERE session_id = " + SQLQuote(s) + " ORDER BY created_timestamp, id"
	}, Read: GooseRows},
	"amazon-q": {Query: func(s string) string {
		return "SELECT key, value FROM conversations WHERE json_extract(value, '$.conversation_id') = " + SQLQuote(s)
	}, Read: AmazonQRows},
}
