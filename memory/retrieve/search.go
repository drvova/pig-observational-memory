package retrieve

import (
	"math/bits"
	"sort"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// The status tags a search's results carry (docs/brain-memory.md, scenario 35).
const (
	TagBelieved   = "believed"
	TagSuperseded = "superseded"
	TagRetracted  = "retracted"
	TagContested  = "contested"
	TagHeld       = "held"
)

// Tags are the status tags in the order docs/protocol.md lists them.
var Tags = []string{TagBelieved, TagSuperseded, TagRetracted, TagContested, TagHeld}

// IsTag reports one of Tags.
func IsTag(s string) bool {
	for _, t := range Tags {
		if t == s {
			return true
		}
	}
	return false
}

// TagOf is a record's status tag, the first that holds of: retracted (its status, or a
// retraction withdrew it), superseded (a record supersedes it), held (ledger.HeldRule), contested
// (ledger.Contested), believed.
func TagOf(o ledger.Observation, superseded, retracted, contested bool) string {
	switch {
	case retracted || o.Status == ledger.StatusRetracted:
		return TagRetracted
	case superseded:
		return TagSuperseded
	case ledger.HeldRule(o) != "":
		return TagHeld
	case contested:
		return TagContested
	}
	return TagBelieved
}

// TagsOf are the status tags of records (the active records, in ledger order), by id.
func TagsOf(records []ledger.Observation) map[string]string {
	superseded := map[string]bool{}
	for _, o := range records {
		for _, id := range o.Supersedes {
			if id != o.ID {
				superseded[id] = true
			}
		}
	}
	retracted := ledger.RetractedIDs(records)
	contested := ledger.Contested(records)
	out := make(map[string]string, len(records))
	for _, o := range records {
		_, c := contested[o.ID]
		out[o.ID] = TagOf(o, superseded[o.ID], retracted[o.ID], c)
	}
	return out
}

// withdrawn reports a tag no current record has: a withdrawn record never outranks a current one.
func withdrawn(tag string) bool { return tag == TagSuperseded || tag == TagRetracted }

// Doc is one record as a search reads it.
type Doc struct {
	ID string
	// Text is what the lexical terms are read from.
	Text string
	// Hay is the lowercased text the searched words are found in as substrings (the read's
	// columns, as the query language always matched).
	Hay string
	// Keys are the record's exact keys (ExactKeys), matched byte for byte.
	Keys []string
	// Episode and Source are the record's episode and first source entry, which neighbours share.
	Episode, Source string
	// Tag is the record's status tag (TagOf).
	Tag string
}

// DocOf is a record as a search reads it, its content as shown (content): Text is the content,
// subject, scope, tool, target and keys, one per line; Hay is Text lowercased.
func DocOf(o ledger.Observation, content, tag string) Doc {
	parts := []string{content}
	for _, s := range []string{o.Subject, o.Scope, o.Tool, o.Target} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	for _, kind := range ledger.KeyKinds {
		parts = append(parts, o.Keys.Get(kind)...)
	}
	text := strings.Join(parts, "\n")
	source := ""
	if len(o.SourceEntryIDs) > 0 {
		source = o.SourceEntryIDs[0]
	}
	return Doc{ID: o.ID, Text: text, Hay: strings.ToLower(text), Keys: ExactKeys(o, content), Episode: o.Episode, Source: source, Tag: tag}
}

// ExactKeys are what the exact index holds for a record, each once, in this order: its keys (in
// ledger.KeyKinds order, as stored), its target and a value target's value (after the first
// colon), the exact-only tokens of content (versions, SHAs, dotted numbers), and content's error
// lines, normalized; every line of an evidence record (a failed tool result's verbatim lines)
// counts as an error line.
func ExactKeys(o ledger.Observation, content string) []string {
	var out []string
	add := func(s string) {
		if s != "" {
			out = appendOnce(out, s)
		}
	}
	for _, kind := range ledger.KeyKinds {
		for _, k := range o.Keys.Get(kind) {
			add(k)
		}
	}
	if o.Target != "" {
		add(o.Target)
		if _, value, ok := strings.Cut(o.Target, ":"); ok && o.Kind == ledger.KindValue {
			add(value)
		}
	}
	_, exact := Tokens(content)
	for _, t := range exact {
		add(t)
	}
	for _, line := range ErrorLines(content, o.Kind == ledger.KindEvidence) {
		add(line)
	}
	return out
}

// Lookups are what a searched string looks up in the exact index, each once: the string
// normalized as a line (NormalizeLine), then each of its raw tokens.
func Lookups(q string) []string {
	var out []string
	if n := NormalizeLine(q); n != "" {
		out = append(out, n)
	}
	for _, t := range RawTokens(q) {
		out = appendOnce(out, t)
	}
	return out
}

// The integer BM25 (docs/protocol.md, "Lexical score"): k1 = 1.2 and b = 0.75 as fractions, the
// IDF as log2((N+1)/(df+0.5)) in 16 fractional bits, the saturation in 16 fractional bits, every
// step floored, so both engines rank by the same integers.
const (
	fracBits = 16
	maxTF    = 100
	// RRFK is reciprocal rank fusion's k; a list's rank r adds rrfScale/(RRFK+r), floored.
	RRFK     = 60
	rrfScale = int64(1) << 40
	// Neighbours is how many contiguous records on each side of a hit its result may merge.
	Neighbours = 2
)

// Log2Fixed is log2(p/q) for integers p > q > 0, in 16 fractional bits, floored: the integer
// part from the bit lengths, then 16 bits by repeated squaring of a 30-bit mantissa.
func Log2Fixed(p, q int64) int64 {
	n := int64(bits.Len64(uint64(p)) - bits.Len64(uint64(q)))
	if q<<n > p {
		n--
	}
	m := (p << 30) / (q << n)
	var frac int64
	for i := 0; i < fracBits; i++ {
		m = (m * m) >> 30
		frac <<= 1
		if m >= 1<<31 {
			m >>= 1
			frac |= 1
		}
	}
	return n<<fracBits | frac
}

// idf is a term's weight in N documents df of which hold it: log2((2N+2)/(2df+1)).
func idf(n, df int64) int64 { return Log2Fixed(2*n+2, 2*df+1) }

// saturation is BM25's tf(k1+1)/(tf+k1(1-b+b·dl/avgdl)) with k1 = 6/5 and b = 3/4, in 16
// fractional bits: tf·44·L·2^16 / (20·tf·L + 6·L + 18·dl·N), L the pool's total length.
func saturation(tf, dl, n, total int64) int64 {
	tf = min(tf, maxTF)
	return (tf * 44 * total << fracBits) / (20*tf*total + 6*total + 18*dl*n)
}

func rrf(rank int) int64 { return rrfScale / int64(RRFK+rank) }

// Result is one result of a search: the hit's index in the pool, how it was found, and the
// contiguous records merged into it (indexes, in pool order, the hit left out).
type Result struct {
	Hit  int
	With []int
	// Exact is true for a hit the exact key index found.
	Exact bool
}

// analyzed is a pool document's terms and exact keys as a search reads them.
type analyzed struct {
	tf    map[string]int32
	dl    int64
	exact map[string]bool
}

func analyze(d Doc) analyzed {
	terms, _ := Tokens(d.Text)
	a := analyzed{tf: make(map[string]int32, len(terms)), dl: int64(len(terms)), exact: make(map[string]bool, len(d.Keys))}
	for _, t := range terms {
		a.tf[t]++
	}
	for _, k := range d.Keys {
		a.exact[k] = true
	}
	return a
}

// query is one searched string read for matching and scoring.
type query struct {
	words   []string   // lowercased white-space words, matched as substrings of Hay
	tokens  [][]string // each raw token's terms
	raw     []string   // each raw token
	lookups []string
}

func readQuery(q string) query {
	out := query{words: strings.Fields(strings.ToLower(q)), lookups: Lookups(q)}
	for _, t := range RawTokens(q) {
		out.raw = append(out.raw, t)
		out.tokens = append(out.tokens, TokenTerms(t))
	}
	return out
}

// tokenHolds reports a raw token the document holds: the token itself as an exact key, its whole
// term, or every one of its other terms (its segments and camelCase parts).
func tokenHolds(a analyzed, raw string, terms []string) bool {
	if a.exact[raw] {
		return true
	}
	if len(terms) == 0 {
		return false
	}
	if a.tf[terms[0]] > 0 {
		return true
	}
	if len(terms) == 1 {
		return false
	}
	for _, t := range terms[1:] {
		if a.tf[t] == 0 {
			return false
		}
	}
	return true
}

// wordHolds reports a searched word the document holds: as a substring of its hay, or through
// every raw token of the word.
func wordHolds(d Doc, a analyzed, word string) bool {
	if strings.Contains(d.Hay, word) {
		return true
	}
	raws := RawTokens(word)
	if len(raws) == 0 {
		return false
	}
	for _, raw := range raws {
		terms := TokenTerms(raw)
		if !a.exact[raw] && len(terms) == 0 {
			return false
		}
		if !tokenHolds(a, raw, terms) {
			return false
		}
	}
	return true
}

func exactCount(a analyzed, q query) int {
	n := 0
	for _, l := range q.lookups {
		if a.exact[l] {
			n++
		}
	}
	return n
}

// matches reports a document a searched string finds: an exact key hit, or every word held
// (wordHolds).
func matches(d Doc, a analyzed, q query) bool {
	if exactCount(a, q) > 0 {
		return true
	}
	if len(q.words) == 0 {
		return false
	}
	for _, w := range q.words {
		if !wordHolds(d, a, w) {
			return false
		}
	}
	return true
}

// Search ranks pool (the read's rows after every other filter, in ledger order) by searches
// (docs/protocol.md, "Retrieval"). A document matches when every searched string finds it (an
// exact key hit, or each word as a substring or through its tokens). When none matches, partial
// is true and the documents holding any searched word are ranked, most words first. The order
// is: current before withdrawn (superseded, retracted), exact hits before lexical ones, the
// fused score (each string's exact list and lexical list, RRF with k = 60), then newest first.
// Each result merges up to Neighbours contiguous pool records on each side that share the hit's
// episode or first source entry; a record merged into a better result is never a result itself.
func Search(pool []Doc, searches []string) (results []Result, partial bool) {
	docs := make([]analyzed, len(pool))
	for i, d := range pool {
		docs[i] = analyze(d)
	}
	return search(pool, docs, searches)
}

func search(pool []Doc, docs []analyzed, searches []string) ([]Result, bool) {
	qs := make([]query, len(searches))
	for i, s := range searches {
		qs[i] = readQuery(s)
	}
	n := int64(len(pool))
	var total int64
	for _, a := range docs {
		total += a.dl
	}
	// The fused score: per string, the exact list and the lexical list.
	score := make([]int64, len(pool))
	exact := make([]bool, len(pool))
	for _, q := range qs {
		type ranked struct {
			i   int
			key int64
		}
		var exactList, lexList []ranked
		var terms []string
		for _, ts := range q.tokens {
			for _, t := range ts {
				terms = appendOnce(terms, t)
			}
		}
		weights := make([]int64, len(terms))
		for k, t := range terms {
			var df int64
			for _, a := range docs {
				if a.tf[t] > 0 {
					df++
				}
			}
			if df > 0 {
				weights[k] = idf(n, df)
			}
		}
		for i, a := range docs {
			if c := exactCount(a, q); c > 0 {
				exactList = append(exactList, ranked{i, int64(c)})
				exact[i] = true
			}
			var s int64
			for k, t := range terms {
				if tf := a.tf[t]; tf > 0 {
					s += weights[k] * saturation(int64(tf), a.dl, n, total) >> fracBits
				}
			}
			if s > 0 {
				lexList = append(lexList, ranked{i, s})
			}
		}
		for _, list := range [][]ranked{exactList, lexList} {
			sort.SliceStable(list, func(x, y int) bool {
				if list[x].key != list[y].key {
					return list[x].key > list[y].key
				}
				return list[x].i > list[y].i
			})
			for r, e := range list {
				score[e.i] += rrf(r + 1)
			}
		}
	}
	var hits []int
	for i := range pool {
		ok := len(qs) > 0
		for _, q := range qs {
			if !matches(pool[i], docs[i], q) {
				ok = false
				break
			}
		}
		if ok {
			hits = append(hits, i)
		}
	}
	partial := false
	words := map[int]int{}
	if len(hits) == 0 {
		var all []string
		for _, q := range qs {
			for _, w := range q.words {
				all = appendOnce(all, w)
			}
		}
		for i := range pool {
			c := 0
			for _, w := range all {
				if wordHolds(pool[i], docs[i], w) {
					c++
				}
			}
			if c > 0 {
				hits = append(hits, i)
				words[i] = c
			}
		}
		partial = len(hits) > 0
	}
	sort.SliceStable(hits, func(x, y int) bool {
		a, b := hits[x], hits[y]
		if words[a] != words[b] {
			return words[a] > words[b]
		}
		if wa, wb := withdrawn(pool[a].Tag), withdrawn(pool[b].Tag); wa != wb {
			return wb
		}
		if exact[a] != exact[b] {
			return exact[a]
		}
		if score[a] != score[b] {
			return score[a] > score[b]
		}
		return a > b
	})
	return merge(pool, hits, exact), partial
}

// related reports two records a result merges: the same episode or the same first source entry.
func related(a, b Doc) bool {
	return a.Episode != "" && a.Episode == b.Episode || a.Source != "" && a.Source == b.Source
}

func merge(pool []Doc, hits []int, exact []bool) []Result {
	taken := make(map[int]bool, len(hits))
	var out []Result
	for _, h := range hits {
		if taken[h] {
			continue
		}
		taken[h] = true
		r := Result{Hit: h, Exact: exact[h]}
		var left []int
		for i := h - 1; i >= 0 && h-i <= Neighbours && !taken[i] && related(pool[h], pool[i]); i-- {
			left = append(left, i)
		}
		for k := len(left) - 1; k >= 0; k-- {
			r.With = append(r.With, left[k])
			taken[left[k]] = true
		}
		for i := h + 1; i < len(pool) && i-h <= Neighbours && !taken[i] && related(pool[h], pool[i]); i++ {
			r.With = append(r.With, i)
			taken[i] = true
		}
		out = append(out, r)
	}
	return out
}
