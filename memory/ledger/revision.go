package ledger

import "encoding/json"

// OMRevisionsRecorded is the entry level 0 appends with the revisions of the files a stretch of
// the branch wrote or edited (extract's revisions; docs/memory-v2.md, "Revisions").
const OMRevisionsRecorded = "om.revisions.recorded"

// Revision is one successful write or edit of a file: its path key, the entry the call sits in,
// the file's previous revision on the branch (Prev, "" for the first), the lines the call added
// and removed, its first changed line (redacted), and the standing records about the file it
// links (decisions, conventions and the file record). ID chains Prev (extract.RevisionID).
type Revision struct {
	ID      string   `json:"id"`
	Path    string   `json:"path"`
	Entry   string   `json:"entry"`
	Prev    string   `json:"prev"`
	Added   int      `json:"added"`
	Removed int      `json:"removed"`
	Summary string   `json:"summary"`
	Links   []string `json:"links,omitempty"`
}

// RevisionsRecordedData is an om.revisions.recorded payload.
type RevisionsRecordedData struct {
	Revisions []Revision `json:"revisions"`
}

// RevisionsRecordedEntry reads a valid om.revisions.recorded entry: every revision with an id and
// a path.
func RevisionsRecordedEntry(entry Entry) (RevisionsRecordedData, bool) {
	if entry.Type != "custom" || entry.CustomType != OMRevisionsRecorded {
		return RevisionsRecordedData{}, false
	}
	raw, err := json.Marshal(entry.Data)
	if err != nil {
		return RevisionsRecordedData{}, false
	}
	var data RevisionsRecordedData
	if json.Unmarshal(raw, &data) != nil {
		return RevisionsRecordedData{}, false
	}
	for _, r := range data.Revisions {
		if r.ID == "" || r.Path == "" {
			return RevisionsRecordedData{}, false
		}
	}
	return data, true
}

// FileRevisions are path's revisions on entries, oldest first.
func FileRevisions(entries []Entry, path string) []Revision {
	var out []Revision
	for _, e := range entries {
		if data, ok := RevisionsRecordedEntry(e); ok {
			for _, r := range data.Revisions {
				if r.Path == path {
					out = append(out, r)
				}
			}
		}
	}
	return out
}
