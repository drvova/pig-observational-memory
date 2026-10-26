// Package ids derives the deterministic memory record ids.
package ids

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// HashID is the first 12 hex characters of the SHA-256 of content.
func HashID(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:12]
}

// The id formats the memory reads accept from code (docs/code-tool.md, "Errors"): a record id
// (HashID's twelve hex characters) and a PiG entry id (the first eight hex characters of a
// random UUID, or a whole UUID when the short form collided). None contains a backtick, so a
// snippet writes every id as a Go raw string.
var (
	RecordIDPattern = regexp.MustCompile(`^[a-f0-9]{12}$`)
	EntryIDPattern  = regexp.MustCompile(`^(?:[a-f0-9]{8}|[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})$`)
)

// OffPathSuffix follows an id that is not on the current branch.
const OffPathSuffix = " (off-path)"

// Valid reports whether id is a record id or an entry id, optionally followed by OffPathSuffix.
func Valid(id string) bool {
	id = strings.TrimSuffix(id, OffPathSuffix)
	return RecordIDPattern.MatchString(id) || EntryIDPattern.MatchString(id)
}
