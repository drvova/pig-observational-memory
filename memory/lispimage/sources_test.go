package lispimage

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var writeSources = flag.Bool("write", false, "copy the repository's lisp/ sources into sources/")

// repoSources lists the files of the repository's lisp/ the embedded copy must hold (relative,
// slash-separated), or skips when the module is checked out without the repository.
func repoSources(t *testing.T) (string, []string) {
	t.Helper()
	lisp, err := filepath.Abs(filepath.Join("..", "..", "lisp"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lisp, "om-engine.asd")); err != nil {
		t.Skip("no lisp/ beside the module")
	}
	files := append([]string(nil), SourceFiles...)
	err = filepath.WalkDir(filepath.Join(lisp, "src"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			rel, _ := filepath.Rel(lisp, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return lisp, files
}

// The embedded sources are lisp/'s, file for file (regenerate with -args -write).
func TestSourcesMatchLisp(t *testing.T) {
	lisp, files := repoSources(t)
	if *writeSources {
		if err := os.RemoveAll(sourceRoot); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			raw, err := os.ReadFile(filepath.Join(lisp, filepath.FromSlash(f)))
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(sourceRoot, filepath.FromSlash(f))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	var embedded []string
	_ = fs.WalkDir(sources, sourceRoot, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			embedded = append(embedded, strings.TrimPrefix(p, sourceRoot+"/"))
		}
		return nil
	})
	sort.Strings(embedded)
	if strings.Join(embedded, "\n") != strings.Join(files, "\n") {
		t.Fatalf("the embedded sources list other files than lisp/; run `go test ./lispimage -run Sources -args -write`\nembedded: %v\nlisp/: %v", embedded, files)
	}
	for _, f := range files {
		want, err := os.ReadFile(filepath.Join(lisp, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		got, _ := sources.ReadFile(sourceRoot + "/" + f)
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from lisp/%s; run `go test ./lispimage -run Sources -args -write`", f, f)
		}
	}
}
