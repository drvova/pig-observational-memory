package lispimage

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// sources is the Lisp core's build inputs (lisp/SBCL_VERSION, the build scripts, the ASDF system
// and every file under lisp/src), copied from the repository's lisp/ by
// `cd memory && go test ./lispimage -run Sources -args -write` and checked against it by the same
// test without -write: om and the PiG adapter carry the sources and build the image on first use
// (docs/lisp-build.md), since no release ships one.
//
//go:embed sources
var sources embed.FS

// sourceRoot is the embedded copy's directory, which stands for the repository's lisp/.
const sourceRoot = "sources"

// SourceFiles are the files of lisp/ the embedded copy holds, besides everything under src/.
var SourceFiles = []string{"SBCL_VERSION", "build-image.sh", "build-image.lisp", "load.lisp", "om-engine.asd"}

var buildID = sync.OnceValue(func() string {
	var files []string
	_ = fs.WalkDir(sources, path.Join(sourceRoot, "src"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(p, ".lisp") || strings.HasSuffix(p, ".json")) {
			files = append(files, strings.TrimPrefix(p, sourceRoot+"/"))
		}
		return nil
	})
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		raw, err := sources.ReadFile(path.Join(sourceRoot, f))
		if err != nil {
			return ""
		}
		h.Write([]byte(f))
		h.Write([]byte{0})
		h.Write(raw)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
})

// BuildID is the build id of an image built from the embedded sources: what the image answers
// in initialize and writes beside itself (lisp/src/server/build.lisp computes the same hash, the
// first 16 hex digits of the SHA-256 over each src file's path relative to lisp/, a NUL, its bytes
// and a NUL, the .lisp and .json files sorted by path).
func BuildID() string { return buildID() }

// extractSources writes the embedded copy into dir, as lisp/ is laid out.
func extractSources(dir string) error {
	return fs.WalkDir(sources, sourceRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, sourceRoot), "/")
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		raw, err := sources.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o600)
	})
}
