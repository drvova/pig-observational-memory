package curated

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"sync"
)

// The store's ledgers (usage.jsonl and models/<model>.jsonl) are append-only and read on every
// memory tool call, agent start and memory-worker pass. Each process keeps what it parsed of
// each ledger with the offset it reached, so a later read parses only the lines appended since
// (by any process) instead of the whole ledger again.
var (
	usageLines  = &appendCache[usageLine]{}
	modelEvents = &appendCache[ModelEvent]{}
)

// tailCheck is how many bytes at the start of a ledger and before a cached offset are kept and
// compared on the next read: a ledger rewritten in place (not appended to) no longer matches,
// and is read again whole. The store only ever appends to its ledgers.
const tailCheck = 64

// appendCache caches the parsed lines of append-only JSON-lines files by path.
type appendCache[T any] struct {
	mu    sync.Mutex
	files map[string]*appendFile[T]
}

// appendFile is one file's parsed lines up to offset, the end of its last complete line.
type appendFile[T any] struct {
	info   os.FileInfo
	offset int64
	// head and tail are the file's first bytes and the bytes just before offset, as read.
	head, tail []byte
	values     []T
}

// read returns the values of path's complete lines, parse deciding each line's value (false
// skips the line). A missing file has none. A line still being written (no newline yet) is
// left for a later read, so a concurrent append is never parsed half-written. The slice
// returned is shared with the cache and must not be modified.
func (c *appendCache[T]) read(path string, parse func(line []byte) (T, bool)) ([]T, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		delete(c.files, path)
		return []T{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if c.files == nil {
		c.files = map[string]*appendFile[T]{}
	}
	entry := c.files[path]
	if entry == nil || !os.SameFile(entry.info, info) || info.Size() < entry.offset || !entry.unchanged(f) {
		entry = &appendFile[T]{values: []T{}}
	}
	if info.Size() > entry.offset {
		data := make([]byte, info.Size()-entry.offset)
		n, err := f.ReadAt(data, entry.offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		data = data[:n]
		if end := bytes.LastIndexByte(data, '\n'); end >= 0 {
			for _, line := range bytes.Split(data[:end], []byte{'\n'}) {
				if v, ok := parse(line); ok {
					entry.values = append(entry.values, v)
				}
			}
			if entry.offset == 0 {
				entry.head = append([]byte(nil), data[:min(end+1, tailCheck)]...)
			}
			entry.offset += int64(end + 1)
			entry.tail = append([]byte(nil), data[max(0, end+1-tailCheck):end+1]...)
		}
	}
	entry.info = info
	c.files[path] = entry
	return slices.Clip(entry.values), nil
}

// unchanged reports whether f still holds the bytes the entry read at its start and before
// its offset.
func (e *appendFile[T]) unchanged(f *os.File) bool {
	if e.offset == 0 {
		return true
	}
	return holds(f, 0, e.head) && holds(f, e.offset-int64(len(e.tail)), e.tail)
}

// holds reports whether f has want at offset at.
func holds(f *os.File, at int64, want []byte) bool {
	got := make([]byte, len(want))
	if _, err := f.ReadAt(got, at); err != nil {
		return false
	}
	return bytes.Equal(got, want)
}

// readUsageLines is the store's usage ledger, every line with a name, in the order written.
func (s Store) readUsageLines() ([]usageLine, error) {
	return usageLines.read(s.usagePath(), func(raw []byte) (usageLine, bool) {
		var line usageLine
		if json.Unmarshal(raw, &line) != nil || line.Name == "" {
			return usageLine{}, false
		}
		return line, true
	})
}
