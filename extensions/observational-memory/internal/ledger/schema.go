package ledger

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// The note schema (docs/memory-interface.md, sections 2 and 8): the fields a model's notes
// carry beyond the kernel's (id, timestamp, content, sources, relevance), declared as data. The
// seed declares the four structured fields every note can carry (kind, subject, scope,
// supersedes); a model's own schema can keep, narrow or leave out those and add fields of its
// own, which are validated against their declaration and stored with the note
// (Observation.Fields). The kernel fields are never the schema's to declare.

// SchemaTable is the table a schema declaration holds, one row per field.
const SchemaTable = "schema"

// SchemaColumns are a schema declaration's columns.
var SchemaColumns = []string{"field", "type", "values", "max", "description"}

// The field types a schema declares.
const (
	// FieldText is single-line text, clipped to the field's max characters.
	FieldText = "text"
	// FieldEnum is one of the field's values.
	FieldEnum = "enum"
	// FieldNumber is a decimal number.
	FieldNumber = "number"
	// FieldDate is a YYYY-MM-DD date.
	FieldDate = "date"
	// FieldIDs is a list of note ids; only supersedes has it.
	FieldIDs = "ids"
)

// The structured fields the kernel stores in Observation's own fields. Each has a fixed type;
// a schema can leave one out or narrow kind's values, never retype it.
const (
	NoteFieldKind       = "kind"
	NoteFieldSubject    = "subject"
	NoteFieldScope      = "scope"
	NoteFieldSupersedes = "supersedes"
)

// builtinTypes are the structured fields' fixed types.
var builtinTypes = map[string]string{NoteFieldKind: FieldEnum, NoteFieldSubject: FieldText, NoteFieldScope: FieldText, NoteFieldSupersedes: FieldIDs}

// reservedNoteFields are the names a schema field cannot take: the kernel's note fields, the
// record's own bookkeeping, and the notes source's fields memory v2's level 0 fills (status,
// event_date and the first source entry's id, role and time), which would shadow a schema
// field of the same name.
var reservedNoteFields = []string{"id", "timestamp", "content", "relevance", "sources", "sourceentryids", "tokencount", "fields", "status", "event_date", "source", "source_role", "source_time"}

// Schema bounds: a note stays a short record, and a schema a short declaration.
const (
	MaxSchemaFields     = 12
	MaxFieldChars       = 200
	DefaultFieldChars   = 80
	MaxEnumValues       = 32
	MaxFieldDescription = 200
)

var schemaFieldName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// SchemaAddedFieldName reports whether name can be a field a note schema adds beyond the
// kernel's and the structured ones (stored in Observation.Fields): a valid schema field name
// that is neither a kernel note field nor one of the structured fields.
func SchemaAddedFieldName(name string) bool {
	_, builtin := builtinTypes[name]
	return schemaFieldName.MatchString(name) && !builtin && !containsString(reservedNoteFields, name)
}

// SchemaField is one declared field.
type SchemaField struct {
	Name, Type string
	// Values are an enum's values.
	Values []string
	// Max bounds a text field's characters.
	Max         int
	Description string
}

// Builtin reports whether the field is one the kernel stores in Observation's own fields.
func (f SchemaField) Builtin() bool { _, ok := builtinTypes[f.Name]; return ok }

// NoteSchema is one parsed schema declaration.
type NoteSchema struct {
	Fields []SchemaField
}

// ParseNoteSchema reads a schema declaration (SchemaTable in the rows codec) and checks it.
func ParseNoteSchema(text string) (NoteSchema, error) {
	decoded, err := tables.Rows.Decode(strings.TrimSpace(text))
	if err != nil {
		return NoteSchema{}, fmt.Errorf("a schema declaration is one %s table in the rows codec: %w", SchemaTable, err)
	}
	if len(decoded) != 1 || decoded[0].Name != SchemaTable {
		return NoteSchema{}, fmt.Errorf("a schema declaration is one %s table with the columns %s", SchemaTable, strings.Join(SchemaColumns, ","))
	}
	t := decoded[0]
	for _, c := range t.Fields {
		if !containsString(SchemaColumns, c) {
			return NoteSchema{}, fmt.Errorf("unknown schema column %q; the columns are %s", c, strings.Join(SchemaColumns, ","))
		}
	}
	if t.Index("field") < 0 || t.Index("type") < 0 {
		return NoteSchema{}, fmt.Errorf("a schema needs the columns field and type")
	}
	if len(t.Rows) > MaxSchemaFields {
		return NoteSchema{}, fmt.Errorf("a schema declares at most %d fields (this one %d)", MaxSchemaFields, len(t.Rows))
	}
	s := NoteSchema{Fields: []SchemaField{}}
	seen := map[string]bool{}
	for r := range t.Rows {
		cell := func(c string) string { v, _ := t.Cell(r, c); return v }
		f := SchemaField{Name: cell("field"), Type: cell("type"), Values: nonEmptyFields(cell("values")), Description: strings.Join(strings.Fields(cell("description")), " ")}
		if v := cell("max"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return NoteSchema{}, fmt.Errorf("schema field %s: max %q must be a whole number", f.Name, v)
			}
			f.Max = n
		}
		if err := checkSchemaField(&f); err != nil {
			return NoteSchema{}, err
		}
		if seen[f.Name] {
			return NoteSchema{}, fmt.Errorf("schema field %s is declared twice", f.Name)
		}
		seen[f.Name] = true
		s.Fields = append(s.Fields, f)
	}
	return s, nil
}

// nonEmptyFields splits a space-separated list; nil when it is empty.
func nonEmptyFields(s string) []string {
	if f := strings.Fields(s); len(f) > 0 {
		return f
	}
	return nil
}

func checkSchemaField(f *SchemaField) error {
	if !schemaFieldName.MatchString(f.Name) {
		return fmt.Errorf("schema field %q must be a lowercase letter then letters, digits or _, at most 32 characters", f.Name)
	}
	if containsString(reservedNoteFields, strings.ToLower(f.Name)) {
		return fmt.Errorf("schema field %s is a kernel note field; a schema only adds to the kernel's", f.Name)
	}
	if n := utf8.RuneCountInString(f.Description); n > MaxFieldDescription {
		return fmt.Errorf("schema field %s: the description is at most %d characters (this one %d)", f.Name, MaxFieldDescription, n)
	}
	if fixed, ok := builtinTypes[f.Name]; ok {
		if f.Type != fixed {
			return fmt.Errorf("schema field %s has the fixed type %s", f.Name, fixed)
		}
		if f.Max != 0 {
			return fmt.Errorf("schema field %s takes no max; the kernel normalizes it", f.Name)
		}
		if f.Name != NoteFieldKind && len(f.Values) > 0 {
			return fmt.Errorf("schema field %s takes no values", f.Name)
		}
		if f.Name == NoteFieldKind {
			if len(f.Values) == 0 {
				return fmt.Errorf("schema field kind needs its values, some of %s", kindValuesText())
			}
			for _, v := range f.Values {
				if !IsObservationKind(v) {
					return fmt.Errorf("schema field kind value %q is not a kind the kernel knows (%s)", v, kindValuesText())
				}
			}
		}
		return nil
	}
	switch f.Type {
	case FieldText:
		if f.Max == 0 {
			f.Max = DefaultFieldChars
		}
		if f.Max < 1 || f.Max > MaxFieldChars {
			return fmt.Errorf("schema field %s: max %d must be 1 to %d characters", f.Name, f.Max, MaxFieldChars)
		}
	case FieldEnum:
		if len(f.Values) == 0 || len(f.Values) > MaxEnumValues {
			return fmt.Errorf("schema field %s: an enum needs 1 to %d values", f.Name, MaxEnumValues)
		}
		for _, v := range f.Values {
			if !schemaFieldName.MatchString(v) {
				return fmt.Errorf("schema field %s: enum value %q must be lowercase letters, digits or _", f.Name, v)
			}
		}
	case FieldNumber, FieldDate:
	default:
		return fmt.Errorf("schema field %s: type %q must be %s, %s, %s or %s", f.Name, f.Type, FieldText, FieldEnum, FieldNumber, FieldDate)
	}
	if f.Type != FieldText && f.Max != 0 {
		return fmt.Errorf("schema field %s: only text takes max", f.Name)
	}
	if f.Type != FieldEnum && len(f.Values) > 0 {
		return fmt.Errorf("schema field %s: only enum takes values", f.Name)
	}
	return nil
}

func kindValuesText() string {
	out := make([]string, 0, len(ObservationKindValues))
	for _, k := range ObservationKindValues {
		out = append(out, string(k))
	}
	return strings.Join(out, ", ")
}

// Field is the declared field name.
func (s NoteSchema) Field(name string) (SchemaField, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return SchemaField{}, false
}

// Names are the declared fields' names, in order.
func (s NoteSchema) Names() []string {
	out := make([]string, 0, len(s.Fields))
	for _, f := range s.Fields {
		out = append(out, f.Name)
	}
	return out
}

// AllowsKind reports whether the schema declares kind with value among its values.
func (s NoteSchema) AllowsKind(value string) bool {
	f, ok := s.Field(NoteFieldKind)
	return ok && containsString(f.Values, value)
}

var numberValue = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// NumberPattern is the pattern a number field's value matches.
const NumberPattern = `^-?[0-9]+(\.[0-9]+)?$`

// DatePattern is the pattern a date field's value matches.
const DatePattern = `^[0-9]{4}-[0-9]{2}-[0-9]{2}$`

// Normalize is value as a field the model added stores it: text collapsed to one line and
// clipped to max, an enum value lowercased, a number or a date checked. ok is false for a
// value the declaration refuses, or for a structured field (the kernel normalizes those).
func (f SchemaField) Normalize(value string) (string, bool) {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" || f.Builtin() {
		return "", false
	}
	switch f.Type {
	case FieldText:
		if r := []rune(value); len(r) > f.Max {
			value = strings.TrimSpace(string(r[:f.Max]))
		}
		return value, true
	case FieldEnum:
		value = strings.ToLower(value)
		return value, containsString(f.Values, value)
	case FieldNumber:
		return value, numberValue.MatchString(value)
	case FieldDate:
		if _, err := time.Parse("2006-01-02", value); err != nil {
			return "", false
		}
		return value, true
	}
	return "", false
}
