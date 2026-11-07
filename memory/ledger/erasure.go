package ledger

import "strings"

// Erasure (docs/brain-memory.md, decision 11; docs/memory-v2.md, "Erasure and the layer purge"):
// an erase request is a forget request the user worded as an erasure ("erase my home address from
// memory", "purge the staging password from memory", "wipe what I said about the trip"). Its record
// is a forget record whose text opens with ErasurePrefix; its tombstones hold ids only, as every
// forget's do, and memory/erase purges the erased records' text from the files the engine owns.

// OMCue is the entry a cue's delivery appends (extract.CueFor, docs/memory-v2.md "Cues"): the
// record it pointed at, the key that matched and the compaction it followed.
const OMCue = "om.cue"

// ErasurePrefix opens an erase request's forget record; a plain forget's opens with ForgetPrefix.
const (
	ErasurePrefix = "The user asked to erase "
	ForgetPrefix  = "The user asked to forget "
)

// erasureVerbs are the words that make a forget request an erasure.
var erasureVerbs = []string{"erase", "purge", "wipe"}

// IsErasureRequest reports whether a forget request's sentence asks for an erasure: one of its
// words (a run of ASCII letters, lowercased) is erase, purge or wipe.
func IsErasureRequest(sentence string) bool {
	word := func(w string) bool {
		for _, v := range erasureVerbs {
			if w == v {
				return true
			}
		}
		return false
	}
	start := -1
	lower := []byte(sentence)
	for i, c := range lower {
		if c >= 'A' && c <= 'Z' {
			lower[i] = c + 'a' - 'A'
		}
	}
	for i := 0; i <= len(lower); i++ {
		letter := i < len(lower) && lower[i] >= 'a' && lower[i] <= 'z'
		switch {
		case letter && start < 0:
			start = i
		case !letter && start >= 0:
			if word(string(lower[start:i])) {
				return true
			}
			start = -1
		}
	}
	return false
}

// IsErasure reports whether o is an erase request's forget record.
func IsErasure(o Observation) bool {
	return o.Kind == KindForget && strings.HasPrefix(o.Content, ErasurePrefix)
}
