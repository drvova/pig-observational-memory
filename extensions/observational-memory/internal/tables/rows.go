package tables

// Rows is the default codec: `name[N]{f1,f2,...}:` followed by N rows indented two spaces,
// values separated by commas (docs/memory-interface.md, section 1). Besides tables the format
// has scalars (`key: value`) and primitive lists (`key[N]: a,b,c`); the codec parses them so
// a misplaced one gets a precise error, but Decode accepts only tables.
var Rows Codec = rowsCodec{}

type rowsCodec struct{}

func (rowsCodec) Name() string { return "rows" }

func (rowsCodec) Encode(tables ...Table) string {
	blocks := make([]block, len(tables))
	for i, t := range tables {
		blocks[i] = t
	}
	return encodeBlocks(blocks...)
}

func (rowsCodec) Decode(text string) ([]Table, error) {
	tables, _, err := decodeTables(text)
	return tables, err
}

func (rowsCodec) Spec() string {
	return "A table is name[N]{field1,field2,...}: followed by exactly N rows, each on its own line indented two spaces, values separated by commas in header order with no space after a comma.\n" +
		"Leave a value empty for none. Quote a value with \" when it is empty, starts or ends with a space, or has a comma, colon, quote, backslash, | or a line break; inside quotes write \\\" \\\\ \\n \\r \\t."
}

// LongSpec is Spec with examples, for a model that keeps making syntax errors.
func (c rowsCodec) LongSpec() string {
	return c.Spec() + "\n" + `Example with two rows, the second without a scope and with a quoted value:
notes[2]{id,scope,content}:
  a1b2c3d4e5f6,on edX,User finished three courses.
  b2c3d4e5f6a1,,"User bakes: sourdough, rye and focaccia."
Common mistakes: N not equal to the number of rows; a row with more or fewer values than fields (quote values that contain commas); a space after a comma; rows not indented exactly two spaces.`
}

// block is one top-level element of a rows text: a Table, a scalar or a list.
type block interface {
	isBlock()
}

// scalar is `key: value`.
type scalar struct {
	Key   string
	Value string
	// Absent means the key has no value (`key:`), as opposed to the empty string (`key: ""`).
	Absent bool
}

// list is a primitive list, `key[N]: a,b,c`.
type list struct {
	Key   string
	Items []string
	// Absent marks absent items by index; nil or out of bounds means present.
	Absent []bool
}

func (Table) isBlock()  {}
func (scalar) isBlock() {}
func (list) isBlock()   {}

// IsAbsent reports whether item i is absent.
func (l list) IsAbsent(i int) bool {
	return i >= 0 && i < len(l.Absent) && l.Absent[i]
}
