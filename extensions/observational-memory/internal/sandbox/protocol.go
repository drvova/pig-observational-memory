package sandbox

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
)

// The bounds of the protocol and of a result (docs/code-tool.md, "Limits: derived, not tuned").
const (
	// ResultChars bounds a result's printed output, last value and error together, and a crash
	// report: one recall page.
	ResultChars = om.RecallPageChars
	// ArgChars bounds the arguments of one om call together: one recall page, the largest
	// legitimate argument being a curated body, which curated.Check bounds to one page.
	ArgChars = om.RecallPageChars
	// ChildLineBytes bounds a child-to-host line: two recall pages of characters (one result,
	// plus the compiled-chunk mask, at most the input bound) at JSON's worst case of six bytes a
	// character, plus a fixed envelope.
	ChildLineBytes = 12*om.RecallPageChars + envelopeBytes
	// HostLineBytes bounds a host-to-child line: init with a full store, MaxLimit units of one
	// recall page each, at six bytes a character, plus the envelope.
	HostLineBytes = 6*om.MaxLimit*om.RecallPageChars + envelopeBytes
	// envelopeBytes is the room for field names, generation, call id and sequence.
	envelopeBytes = 4096
)

// The message types.
const (
	tInit   = "init"
	tReady  = "ready"
	tRun    = "run"
	tCall   = "call"
	tReply  = "reply"
	tResult = "result"
	tDefine = "define"
)

// The outcomes of a call.
const (
	OutcomeOK        = "ok"
	OutcomeCompile   = "compile"
	OutcomeRuntime   = "runtime"
	OutcomeCancelled = "cancelled"
	OutcomeCrashed   = "crashed"
)

// The chunk kinds on the wire.
const (
	kindStatement   = "statement"
	kindDeclaration = "declaration"
)

// The compiled-chunk mask's characters, one per chunk.
const (
	maskNotRun   = '-' // a statement, an import, or a declaration that never ran
	maskCompiled = 'c' // a declaration that compiled
	maskFailed   = 'x' // a declaration that failed to compile
)

// Unit is one saved definition: its name (f, Recv.Name, a type or constant) and its source.
type Unit struct {
	Name string `json:"name"`
	Src  string `json:"src"`
}

// wireComponent is one component of saved units, evaluated together.
type wireComponent struct {
	ID    int    `json:"id"`
	Units []Unit `json:"units"`
}

// wireFailure is a component that failed to load, with the interpreter's message.
type wireFailure struct {
	ID  int    `json:"id"`
	Msg string `json:"msg"`
}

// wireChunk is one chunk of a call's code as the host split and checked it.
type wireChunk struct {
	Kind string `json:"kind"`
	Src  string `json:"src"`
	// Line is the chunk's first line in the call's own numbering.
	Line int `json:"line"`
	// Names are the names a declaration chunk binds (Recv.Name for a method), so the child
	// can restore their earlier definitions when the chunk's group fails to compile.
	Names []string `json:"names,omitempty"`
}

// msg is one line of the protocol. T is its type; which other fields it carries depends on T.
type msg struct {
	T    string `json:"t"`
	Gen  uint64 `json:"gen"`
	Call uint64 `json:"call,omitempty"`
	Seq  uint64 `json:"seq,omitempty"`

	// init, define.
	Components []wireComponent `json:"components,omitempty"`
	// ready.
	Loaded []int         `json:"loaded,omitempty"`
	Failed []wireFailure `json:"failed,omitempty"`
	OOM    string        `json:"oom,omitempty"`

	// run.
	Chunks  []wireChunk       `json:"chunks,omitempty"`
	Strings map[string]string `json:"strings,omitempty"`
	Last    bool              `json:"last,omitempty"`

	// call, reply.
	M    string          `json:"m,omitempty"`
	Args json.RawMessage `json:"args,omitempty"`
	Res  json.RawMessage `json:"res,omitempty"`
	Err  string          `json:"err,omitempty"`

	// result.
	Out      string `json:"out,omitempty"`
	Val      string `json:"val,omitempty"`
	HasVal   bool   `json:"has_val,omitempty"`
	Outcome  string `json:"outcome,omitempty"`
	Compiled string `json:"compiled,omitempty"`
	Cut      int    `json:"cut,omitempty"`
	Exit     bool   `json:"exit,omitempty"`
}

// errLineOverBound is a line that reached its bound without a newline.
var errLineOverBound = errors.New("protocol message over bound")

// lineReader reads newline-terminated lines of at most max bytes. A line that reaches the bound
// without a newline is never decoded: readLine returns errLineOverBound and holds no more than
// max bytes of it.
type lineReader struct {
	r   *bufio.Reader
	max int
	buf []byte
}

func newLineReader(r io.Reader, max int) *lineReader {
	return &lineReader{r: bufio.NewReaderSize(r, 64<<10), max: max}
}

func (l *lineReader) readLine() ([]byte, error) {
	l.buf = l.buf[:0]
	for {
		frag, err := l.r.ReadSlice('\n')
		if len(l.buf)+len(frag) > l.max+1 {
			return nil, errLineOverBound
		}
		l.buf = append(l.buf, frag...)
		switch {
		case err == nil:
			return l.buf[:len(l.buf)-1], nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			if len(l.buf) > 0 && err == io.EOF {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
	}
}

// readMsg reads and decodes one line.
func (l *lineReader) readMsg() (msg, error) {
	line, err := l.readLine()
	if err != nil {
		return msg{}, err
	}
	var m msg
	if err := json.Unmarshal(line, &m); err != nil {
		return msg{}, err
	}
	return m, nil
}

// encodeLine encodes m as one line, or refuses it when it would exceed max bytes.
func encodeLine(m msg, max int) ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if len(b)+1 > max {
		return nil, errLineOverBound
	}
	return append(b, '\n'), nil
}

// argChars counts the characters of every string value in a call's JSON arguments, at any
// depth (an option's field names are not arguments).
func argChars(raw json.RawMessage) (int, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, err
	}
	return stringChars(v), nil
}

func stringChars(v any) int {
	switch v := v.(type) {
	case string:
		return utf8.RuneCountInString(v)
	case []any:
		n := 0
		for _, e := range v {
			n += stringChars(e)
		}
		return n
	case map[string]any:
		n := 0
		for _, e := range v {
			n += stringChars(e)
		}
		return n
	}
	return 0
}

// valueChars counts the characters of every string a value holds, at any depth, as argChars
// counts them once encoded.
func valueChars(v reflect.Value) int {
	switch v.Kind() {
	case reflect.String:
		return utf8.RuneCountInString(v.String())
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return 0
		}
		return valueChars(v.Elem())
	case reflect.Slice, reflect.Array:
		n := 0
		for i := 0; i < v.Len(); i++ {
			n += valueChars(v.Index(i))
		}
		return n
	case reflect.Map:
		n := 0
		it := v.MapRange()
		for it.Next() {
			n += valueChars(it.Value())
		}
		return n
	case reflect.Struct:
		n := 0
		for i := 0; i < v.NumField(); i++ {
			n += valueChars(v.Field(i))
		}
		return n
	}
	return 0
}

// cutChars keeps at most n characters of s and reports how many it dropped.
func cutChars(s string, n int) (string, int) {
	if n < 0 {
		n = 0
	}
	if utf8.RuneCountInString(s) <= n {
		return s, 0
	}
	i, k := 0, 0
	for k < n {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		k++
	}
	return s[:i], utf8.RuneCountInString(s[i:])
}

// oomSet is the child's report that it made itself the out-of-memory killer's first pick.
const oomSet = "set"
