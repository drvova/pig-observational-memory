package ledger_test

import (
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
)

func TestSubjectTableName(t *testing.T) {
	for subject, want := range map[string]string{
		"baking":          "notes.baking",
		"Gaming  Hours":   "notes.gaming_hours",
		"online courses!": "notes.online_courses",
		"c++ / go":        "notes.c_go",
		"!!":              "",
		"":                "",
	} {
		if got := ledger.SubjectTableName(subject); got != want {
			t.Errorf("SubjectTableName(%q) = %q, want %q", subject, got, want)
		}
	}
}
