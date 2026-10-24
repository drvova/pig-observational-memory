package tables

import (
	"strings"
	"testing"
)

// FuzzRoundTrip asserts decodeAll(encodeBlocks(x)) == x for tables, scalars and lists built from
// arbitrary strings (an absent cell of a single-field table comes back as ""), and that the
// codec's tables survive strings.TrimSpace. Cells are the parts of cells split on "\x1f"; bit
// k of mask marks cell k absent.
func FuzzRoundTrip(f *testing.F) {
	f.Add("notes", "id", "content", "a\x1fcommas, here\x1fb\x1f\"quoted\"\x1fc\x1fline\nbreak", uint64(0))
	f.Add("t", "a", "b", "\x1f\x1f \x1f\\\x1f|:\x1f\r\t", uint64(0b10101))
	f.Add("x.y-z", "f1", "f2", "日本語\x1f🍞", uint64(1))
	f.Add("bad name", "a b", "", "", uint64(0))
	f.Fuzz(func(t *testing.T, name, f1, f2, cells string, mask uint64) {
		if !ValidName(name) {
			name = "t"
		}
		if !ValidName(f1) {
			f1 = "a"
		}
		if !ValidName(f2) || f2 == f1 {
			f2 = f1 + "2"
		}
		vals := strings.Split(cells, "\x1f")
		absentAt := func(k int) bool { return k < 64 && mask&(1<<k) != 0 }

		tb := Table{Name: name, Fields: []string{f1, f2}}
		for k := 0; k+1 < len(vals); k += 2 {
			tb.Rows = append(tb.Rows, []string{vals[k], vals[k+1]})
			tb.Absent = append(tb.Absent, []bool{absentAt(k), absentAt(k + 1)})
		}
		one := Table{Name: name, Fields: []string{f1}}
		lst := list{Key: f1, Items: vals}
		for k, v := range vals {
			one.Rows = append(one.Rows, []string{v})
			one.Absent = append(one.Absent, []bool{absentAt(k)})
			lst.Absent = append(lst.Absent, absentAt(k))
		}
		blocks := []block{
			tb,
			one,
			lst,
			scalar{Key: f2, Value: cells, Absent: absentAt(63)},
		}
		text := encodeBlocks(blocks...)
		got, err := decodeAll(text)
		if err != nil {
			t.Fatalf("decodeAll(encodeBlocks(x)) failed: %v\n%s", err, text)
		}
		if !blocksEqual(got, singleFieldAbsentAsEmpty(blocks)) {
			t.Fatalf("decodeAll(encodeBlocks(x)) != x\ntext:\n%s\ngot %#v\nwant %#v", text, got, blocks)
		}
		trimmed := strings.TrimSpace(Rows.Encode(tb, one))
		tables, err := Rows.Decode(trimmed)
		if err != nil {
			t.Fatalf("Decode(TrimSpace(Encode(x))) failed: %v\n%s", err, trimmed)
		}
		if !blocksEqual([]block{tables[0], tables[1]}, singleFieldAbsentAsEmpty([]block{tb, one})) {
			t.Fatalf("Decode(TrimSpace(Encode(x))) != x\ntext:\n%s", trimmed)
		}
	})
}

// FuzzDecode asserts that Decode never panics, that every failure is an *Error with a
// position inside the text, and that whatever decodes re-encodes to an equal value.
func FuzzDecode(f *testing.F) {
	for _, s := range []string{
		"reflections[2]{id,dates,content}:\n  a,b,c\n  d,e,\"f, g\"",
		"k: v\nl[2]: a,\"b\"\n",
		"t[1]{a}:\n  \"unterminated",
		"t[2]{a,b}:\n  1,2,3",
		"  row",
		"t[1]{a}:\n  \n",
		"x[0]:",
		"s: \"\\q\"",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		blocks, err := decodeAll(text)
		if err != nil {
			e, ok := AsError(err)
			if !ok {
				t.Fatalf("error %T is not *Error: %v", err, err)
			}
			if e.Line < 1 || e.Line > strings.Count(text, "\n")+1 || e.Column < 1 || e.Msg == "" || e.Hint == "" {
				t.Fatalf("bad error position or text: %#v", e)
			}
			return
		}
		again, err := decodeAll(encodeBlocks(blocks...))
		if err != nil {
			t.Fatalf("re-decode failed: %v", err)
		}
		if !blocksEqual(again, singleFieldAbsentAsEmpty(blocks)) {
			t.Fatalf("re-decode differs:\n%#v\n%#v", again, blocks)
		}
	})
}

// singleFieldAbsentAsEmpty returns blocks with every absent cell of a single-field table
// turned into a present "", which is how the encoder writes it.
func singleFieldAbsentAsEmpty(blocks []block) []block {
	out := make([]block, len(blocks))
	for i, blk := range blocks {
		t, ok := blk.(Table)
		if !ok || len(t.Fields) != 1 || t.Absent == nil {
			out[i] = blk
			continue
		}
		rows := make([][]string, len(t.Rows))
		for r, row := range t.Rows {
			rows[r] = row
			if t.IsAbsent(r, 0) {
				rows[r] = []string{""}
			}
		}
		t.Rows, t.Absent = rows, nil
		out[i] = t
	}
	return out
}
