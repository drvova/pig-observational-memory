// Command compactbench replays Compact-Bench's no-model tracks (github.com/cai-jianfeng/
// compact-bench, data caijanfeng/compact-bench on Hugging Face, Apache-2.0) through this
// engine, and scores them with the benchmark's own definitions (docs/bench-results.md,
// "Compact-Bench"). It is a separate implementation: it reads the released data files and
// never runs the benchmark's code.
//
//	compactbench fetch -data DIR                 download e2/sessions.jsonl and e3/chains.jsonl, checking their digests
//	compactbench retention -data DIR [-budgets 16384,0] [-limit N] [-out FILE]
//	compactbench production -data DIR
//
// retention replays every Recurring-Detail Retention session through level 0 and
// selection.Render (no model request) and reports the mean share of the session's recurring
// details the summary holds verbatim, the measure the paper reports as 79.1% for mechanical
// extraction and at most 16.0% for model-written summaries. production scores the recorded
// production summaries of the Production-Chain Retention track; the release holds no history
// before a boundary, so the engine cannot replay those.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/drvova/pig-observational-memory/memory/hostinfo"
)

// dataBase is the pinned dataset revision's download root; dataFiles are the files the
// no-model tracks read, with the SHA-256 the release's SHA256SUMS lists for them.
const dataBase = "https://huggingface.co/datasets/caijanfeng/compact-bench/resolve/main/"

var dataFiles = map[string]string{
	"e2/sessions.jsonl": "5137e63af45513c1b8b7d40960735a8e545e887299ea422562443f58a97d4c09",
	"e3/chains.jsonl":   "bb3460c1130c34147bcb359d9ba117c7009b9bd3e2f7933126b093f57426cccd",
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: compactbench fetch|retention|production -data DIR")
		return 2
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	data := fs.String("data", "", "directory holding e2/sessions.jsonl and e3/chains.jsonl (the dataset's layout)")
	budgets := fs.String("budgets", "16384,0", "summary budgets in estimated tokens; 0 is the engine's own budget (the removed source)")
	limit := fs.Int("limit", 0, "replay only the first N sessions (0 = all)")
	workers := fs.Int("workers", 1, "sessions replayed at once")
	out := fs.String("out", "", "write one JSON line per session and budget here")
	keep := fs.Bool("keep-summaries", false, "include each summary in -out")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *data == "" {
		fmt.Fprintln(stderr, "compactbench: -data is required")
		return 2
	}
	var err error
	switch args[0] {
	case "fetch":
		err = fetch(*data, stderr)
	case "production":
		var chains []Chain
		if chains, err = readJSONL[Chain](filepath.Join(*data, "e3", "chains.jsonl")); err == nil {
			fmt.Fprint(stdout, productionReport(chains))
		}
	case "retention":
		var bs []int
		if bs, err = parseBudgets(*budgets); err == nil {
			err = retention(*data, bs, *limit, *workers, *out, *keep, stdout, stderr)
		}
	default:
		err = fmt.Errorf("unknown command %q", args[0])
	}
	if err != nil {
		fmt.Fprintln(stderr, "compactbench:", err)
		return 1
	}
	return 0
}

func parseBudgets(s string) ([]int, error) {
	var out []int
	for _, f := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("bad budget %q", f)
		}
		out = append(out, n)
	}
	return out, nil
}

func retention(data string, budgets []int, limit, workers int, outPath string, keep bool, stdout, stderr io.Writer) error {
	sessions, err := readJSONL[Session](filepath.Join(data, "e2", "sessions.jsonl"))
	if err != nil {
		return err
	}
	if limit > 0 && limit < len(sessions) {
		sessions = sessions[:limit]
	}
	root, err := os.MkdirTemp("", "compactbench-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	// The engine's config root and every store live in the temporary directory: the replay
	// never reads or writes the user's own memory.
	hostinfo.AgentDir = func() (string, error) { return root, nil }
	var w *bufio.Writer
	if outPath != "" {
		f, err := os.Create(outPath)
		if err != nil {
			return err
		}
		defer f.Close()
		w = bufio.NewWriter(f)
		defer w.Flush()
	}
	var all []RetentionResult
	for _, budget := range budgets {
		results := make([]RetentionResult, len(sessions))
		jobs := make(chan int)
		var wg sync.WaitGroup
		for range max(1, workers) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					results[i] = replaySession(root, sessions[i], budget, keep)
				}
			}()
		}
		for i := range sessions {
			jobs <- i
			fmt.Fprintf(stderr, "\rbudget %d: %d/%d", budget, i+1, len(sessions))
		}
		close(jobs)
		wg.Wait()
		fmt.Fprintln(stderr)
		for _, r := range results {
			if w != nil {
				line, _ := json.Marshal(r)
				w.Write(append(line, '\n'))
			}
		}
		all = append(all, results...)
	}
	fmt.Fprint(stdout, retentionReport(len(sessions), all))
	return nil
}

// retentionReport is the benchmark's retention table (mean of the per-session recalls,
// sessions that failed left out and counted), with the engine's own extra columns.
func retentionReport(n int, results []RetentionResult) string {
	byV := map[string][]RetentionResult{}
	var order []string
	errs := map[string]int{}
	for _, r := range results {
		if _, ok := byV[r.Variant]; !ok {
			order = append(order, r.Variant)
			byV[r.Variant] = nil
		}
		if r.Error != "" {
			errs[r.Variant]++
			continue
		}
		byV[r.Variant] = append(byV[r.Variant], r)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Recurring-Detail Retention, this engine's level 0 (sessions: %d)\n\n", n)
	b.WriteString("| variant | mean recall | n | errors | mean summary chars | ledger recall | source recall | of replayed |\n|---|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, v := range order {
		g := byV[v]
		fmt.Fprintf(&b, "| %s | %.1f%% | %d | %d | %.0f | %.1f%% | %.1f%% | %.1f%% |\n", v,
			100*meanOf(g, func(r RetentionResult) float64 { return r.Recall }), len(g), errs[v],
			meanOf(g, func(r RetentionResult) float64 { return float64(r.SLen) }),
			100*meanOf(g, func(r RetentionResult) float64 { return r.LedgerRecall }),
			100*meanOf(g, func(r RetentionResult) float64 { return r.SourceRecall }),
			100*meanOf(g, func(r RetentionResult) float64 { return r.ReplayedRecall }))
	}
	b.WriteString("\n## per atom kind (atoms pooled over sessions)\n\n| variant | kind | in summary | atoms | share |\n|---|---|---:|---:|---:|\n")
	for _, v := range order {
		pooled := map[string][2]int{}
		for _, r := range byV[v] {
			for k, c := range r.Kinds {
				p := pooled[k]
				p[0] += c[0]
				p[1] += c[1]
				pooled[k] = p
			}
		}
		var kinds []string
		for k := range pooled {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		for _, k := range kinds {
			p := pooled[k]
			fmt.Fprintf(&b, "| %s | %s | %d | %d | %.1f%% |\n", v, k, p[1], p[0], 100*float64(p[1])/float64(max(1, p[0])))
		}
	}
	b.WriteString("\n## per client\n\n| client | variant | mean recall | n |\n|---|---|---:|---:|\n")
	clients := map[string]bool{}
	for _, r := range results {
		clients[r.Client] = true
	}
	var cs []string
	for c := range clients {
		cs = append(cs, c)
	}
	sort.Strings(cs)
	for _, c := range cs {
		for _, v := range order {
			var g []RetentionResult
			for _, r := range byV[v] {
				if r.Client == c {
					g = append(g, r)
				}
			}
			if len(g) > 0 {
				fmt.Fprintf(&b, "| %s | %s | %.1f%% | %d |\n", c, v, 100*meanOf(g, func(r RetentionResult) float64 { return r.Recall }), len(g))
			}
		}
	}
	b.WriteString("\nmean recall: the share of a session's atoms its summary holds verbatim, averaged over sessions (the benchmark's measure). ledger recall: in the summary or in an active record the memory tool reaches. source recall: in the replayed entries at all (system messages are not replayed). of replayed: the summary's share of the atoms the replayed entries hold.\n")
	return b.String()
}

func meanOf(rs []RetentionResult, f func(RetentionResult) float64) float64 {
	if len(rs) == 0 {
		return 0
	}
	t := 0.0
	for _, r := range rs {
		t += f(r)
	}
	return t / float64(len(rs))
}

func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 512<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, v)
	}
	return out, sc.Err()
}

// fetch downloads the pinned data files into dir and checks their digests; a file already
// there with the right digest is kept.
func fetch(dir string, log io.Writer) error {
	names := make([]string, 0, len(dataFiles))
	for name := range dataFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if digest(path) == dataFiles[name] {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		fmt.Fprintln(log, "downloading", name)
		resp, err := http.Get(dataBase + name)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("%s: HTTP %d", name, resp.StatusCode)
		}
		tmp := path + ".part"
		f, err := os.Create(tmp)
		if err == nil {
			_, err = io.Copy(f, resp.Body)
			err = errors.Join(err, f.Close())
		}
		resp.Body.Close()
		if err != nil {
			return err
		}
		if got := digest(tmp); got != dataFiles[name] {
			os.Remove(tmp)
			return fmt.Errorf("%s: digest %s, want %s", name, got, dataFiles[name])
		}
		if err := os.Rename(tmp, path); err != nil {
			return err
		}
	}
	return nil
}

func digest(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}
