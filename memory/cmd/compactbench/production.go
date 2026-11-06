package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Chain is one record of Compact-Bench's e3/chains.jsonl (Production-Chain Retention): the
// summaries a harness wrote at each of a real session's compaction boundaries, with the atoms
// the following requests used. The release holds no history before any boundary, so the
// engine cannot replay a chain; these are scored as recorded, the benchmark's own track.
type Chain struct {
	Sess    string     `json:"sess"`
	Client  string     `json:"client"`
	NBounds int        `json:"n_bounds"`
	PerK    []Boundary `json:"per_k"`
}

// Boundary is one compaction boundary of a chain.
type Boundary struct {
	K         int                 `json:"k"`
	Marker    string              `json:"marker"`
	Form      string              `json:"form"`
	S         string              `json:"S_k"`
	AtomsF    map[string][]string `json:"atomsF"`
	DoneFiles []string            `json:"done_files"`
}

// pendingRE is the benchmark's pending-context pattern (its temporal-disorder check): a file
// already written before the boundary that the summary names within 150 characters of one
// of these words is listed as still to do.
var pendingRE = regexp.MustCompile(`(?i)(待办|待完成|还需要|尚未|未完|pending|to[- ]?do|next steps?|remaining)`)

// BoundaryScore is one boundary's R_k (the share of its atoms the summary holds verbatim) and
// temporal-disorder count.
type BoundaryScore struct {
	K       int      `json:"k"`
	NAtoms  int      `json:"n_atoms_F"`
	R       *float64 `json:"R_k"`
	Triplet int      `json:"triplet_errors"`
	SLen    int      `json:"S_len"`
}

// scoreBoundary scores one recorded summary by the benchmark's definition.
func scoreBoundary(b Boundary) BoundaryScore {
	n, in := 0, 0
	for _, vs := range b.AtomsF {
		for _, v := range vs {
			n++
			if strings.Contains(b.S, v) {
				in++
			}
		}
	}
	out := BoundaryScore{K: b.K, NAtoms: n, SLen: len([]rune(b.S))}
	if n > 0 {
		r := float64(in) / float64(n)
		out.R = &r
	}
	runes := []rune(b.S)
	for _, f := range b.DoneFiles {
		base := f[strings.LastIndex(f, "/")+1:]
		for _, m := range pendingRE.FindAllStringIndex(b.S, -1) {
			start := len([]rune(b.S[:m[0]]))
			end := len([]rune(b.S[:m[1]]))
			ctx := string(runes[max(0, start-150):min(len(runes), end+150)])
			if strings.Contains(ctx, base) {
				out.Triplet++
				break
			}
		}
	}
	return out
}

// productionReport is the benchmark's production table: mean R_k per boundary index, n, the
// temporal-disorder rate, and the mean last/first ratio.
func productionReport(chains []Chain) string {
	byK := map[int][]float64{}
	trip := map[int][]bool{}
	var ratios []float64
	for _, ch := range chains {
		var first, last *float64
		for i, b := range ch.PerK {
			s := scoreBoundary(b)
			if s.R != nil {
				byK[s.K] = append(byK[s.K], *s.R)
				if first == nil {
					first = s.R
				}
			}
			trip[s.K] = append(trip[s.K], s.Triplet > 0)
			if i == len(ch.PerK)-1 {
				last = s.R
			}
		}
		if first != nil && *first != 0 && last != nil {
			ratios = append(ratios, *last / *first)
		}
	}
	var ks []int
	for k := range byK {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	var b strings.Builder
	fmt.Fprintf(&b, "# Production-Chain Retention, recorded summaries (n=%d chains)\n\n", len(chains))
	b.WriteString("| compaction index k | mean R_k | n | temporal-disorder rate |\n|---|---:|---:|---:|\n")
	for _, k := range ks {
		t := 0
		for _, x := range trip[k] {
			if x {
				t++
			}
		}
		fmt.Fprintf(&b, "| %d | %.1f%% | %d | %.1f%% |\n", k, 100*mean(byK[k]), len(byK[k]), 100*float64(t)/float64(len(trip[k])))
	}
	if len(ratios) > 0 {
		fmt.Fprintf(&b, "\n- last/first retention ratio mean = %.2f (n=%d)\n", mean(ratios), len(ratios))
	}
	return b.String()
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	t := 0.0
	for _, x := range xs {
		t += x
	}
	return t / float64(len(xs))
}
