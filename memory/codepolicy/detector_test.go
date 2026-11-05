package codepolicy

import (
	"strings"
	"testing"
)

func TestCheckDetector(t *testing.T) {
	good := []string{
		"func Count() int {\n\tn := 0\n\tfor _, e := range om.History(om.Search(`retry`)).Rows {\n\t\tif e.Role == \"tool\" {\n\t\t\tn++\n\t\t}\n\t}\n\treturn n\n}",
		"type box struct{ n int }\nfunc (b box) twice() int { return b.n * 2 }\nconst k = 2\nfunc Count() int { return box{n: om.Notes(om.Kind(`plan`)).Matched}.twice() / k }",
		"func Count() int { return len(om.Subjects()) + len(strings.Fields(`a b`)) }",
		"func Count() int { n, err := om.NoteByID(`o1`); if err != nil { return 0 }; return len(n.SourceEntryIDs) }",
	}
	for _, src := range good {
		if err := CheckDetector(src); err != nil {
			t.Errorf("refused %q: %v", src, err)
		}
	}
	bad := map[string]string{
		"func Count() int { om.Save(`n`, `lesson`, `d`); return 1 }":       "om.Save",
		"func Count() int { t, _ := om.Recall(`e1`); return len(t.Body) }": "om.Recall",
		"func Count() int { return om.Saved().Len() }":                     "om.Saved",
		"func Count() int { return len(kept()) }":                          "kept",
		"func init() {}\nfunc Count() int { return 0 }":                    "init",
		"func Count() int { go func() {}(); return 0 }":                    "go statement",
		"func Count() string { return `x` }":                               "func Count() int",
		"func Count(n int) int { return n }":                               "func Count() int",
		"func count() int { return 0 }":                                    "exactly one",
		"func Count() int { return 0 }\nfunc Count() int { return 1 }":     "",
		"var x = 3\nfunc Count() int { return x }":                         "variables",
		"func Count() int { return x }":                                    "x is not declared",
		"Count()":                                                          "declarations only",
		"import \"fmt\"\nfunc Count() int { return 0 }":                    "declarations only",
		"func Count() int { len: for { break len }; return 0 }":            "owned",
		"func Count() int { strconv.ErrSyntax = nil; return 0 }":           "assignment through a package",
		"": "empty",
		"func Count() int { return " + strings.Repeat("1+", 6000) + "1 }":            "one recall page",
		"func Count() int { return om.Notes().Rows[0].Recall().Total }":              "",
		"func Count() int { return len(om.Info().String()) + len(om.Layer().Rows) }": "om.Info",
	}
	for src, want := range bad {
		err := CheckDetector(src)
		if err == nil {
			if want == "" {
				// Refused at run time by the host (a record's Recall method) or by the
				// interpreter (a duplicate function): the pure check need not see it.
				continue
			}
			t.Errorf("accepted %q", src)
			continue
		}
		if want != "" && !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want it to name %q", src, err, want)
		}
	}
}
