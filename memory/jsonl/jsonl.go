// Package jsonl appends to the stores' append-only JSON-lines files (the layer's manifest and
// floor record, the usage ledgers, the neutral store), the same way the Lisp image does
// (lisp/src/store/append.lisp), so either can append after the other.
//
// A crash can leave a file's last line torn: the bytes of a write cut off, with no newline.
// Every reader skips a line that does not decode, so the fragment is harmless as it stands;
// appending after it as it stands would glue the next line onto it, and both would be lost. So
// Append ends a torn tail with a newline first: the fragment stays one line no reader decodes,
// and a last line that does decode without its newline stays the line every reader already
// read. Nothing is truncated, so no append changes what a reader read before it. The line goes
// in one write, then fsync, and the directory is synced when the append created the file, so a
// line Append returned is durable: a reader never reads a line a crash later takes back.
package jsonl

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// Append appends line and a newline to path (created 0600). The caller holds the file's lock.
// It reports whether it found a torn tail.
func Append(path string, line []byte) (torn bool, err error) {
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, fs.ErrNotExist)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return false, err
	}
	torn, err = TornTail(f)
	if err != nil {
		f.Close()
		return false, err
	}
	buf := make([]byte, 0, len(line)+2)
	if torn {
		buf = append(buf, '\n')
	}
	buf = append(append(buf, line...), '\n')
	if _, err := f.Write(buf); err != nil {
		f.Close()
		return torn, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return torn, err
	}
	if err := f.Close(); err != nil {
		return torn, err
	}
	if created {
		syncDir(filepath.Dir(path))
	}
	return torn, nil
}

// TornTail reports whether f is non-empty and its last byte is not a newline.
func TornTail(f *os.File) (bool, error) {
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() == 0 {
		return false, nil
	}
	b := make([]byte, 1)
	if _, err := f.ReadAt(b, info.Size()-1); err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return b[0] != '\n', nil
}

// syncDir syncs a directory so an entry created in it is durable; an error is ignored (some
// file systems, and Windows, refuse it).
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
