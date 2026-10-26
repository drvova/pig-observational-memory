package codestore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/codepolicy"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/codestore"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/sandbox"
)

// These tests drive the store through real namespace children (the test binary re-executed:
// linking sandbox puts its init, which catches the child marker, in this binary), wired the way
// the memory tool's keep path will be: the store's components are what every start replays,
// a call keeps the units of the declaration chunks the child reports compiled, and kept and
// forget are answered from the store.

// ns is one extension's namespace over a store: its supervisor, and the digests its live
// namespace loaded or kept.
type ns struct {
	t     *testing.T
	store codestore.Store
	sup   *sandbox.Supervisor

	mu   sync.Mutex
	seen map[string]string
	// stub is the source forget asked the live namespace to evaluate.
	stub string
}

func newNS(t *testing.T, store codestore.Store) *ns {
	n := &ns{t: t, store: store}
	n.sup = sandbox.New(sandbox.Config{Units: n.units})
	t.Cleanup(n.sup.Kill)
	return n
}

// units is the supervisor's Units: the store loaded and checked at every start.
func (n *ns) units() []sandbox.Component {
	l, err := n.store.Load()
	if err != nil {
		n.t.Errorf("load: %v", err)
		return nil
	}
	n.mu.Lock()
	n.seen = l.Digests()
	n.mu.Unlock()
	out := make([]sandbox.Component, len(l.Components))
	for i, c := range l.Components {
		for _, u := range c.Units {
			out[i].Units = append(out[i].Units, sandbox.Unit{Name: u.Name, Src: u.Src})
		}
		out[i].Deps = c.Deps
	}
	return out
}

// Call answers kept and forget from the store.
func (n *ns) Call(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	var args []json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	switch method {
	case omwire.MKept:
		var names []string
		if len(args) > 0 {
			if err := json.Unmarshal(args[0], &names); err != nil {
				return nil, err
			}
		}
		l, err := n.store.Load()
		if err != nil {
			return nil, err
		}
		n.mu.Lock()
		seen := n.seen
		n.mu.Unlock()
		return json.Marshal(codestore.Kept(l, nil, seen, names...))
	case omwire.MForget:
		var name string
		if err := json.Unmarshal(args[0], &name); err != nil {
			return nil, err
		}
		res, err := n.store.Forget(name)
		if err != nil {
			return nil, err
		}
		n.mu.Lock()
		n.stub = res.Stub
		n.mu.Unlock()
		return json.Marshal(res.Text())
	}
	return nil, fmt.Errorf("no %s here", method)
}

// run checks and runs code, then keeps the units of the declaration chunks that compiled.
func (n *ns) run(code string) (sandbox.Result, codestore.KeepResult) {
	n.t.Helper()
	p, err := codepolicy.Check(codepolicy.Input{Code: code})
	if err != nil {
		n.t.Fatalf("policy: %v\n%s", err, code)
	}
	res := n.sup.Run(context.Background(), sandbox.Request{Program: p}, n)
	var compiled []codepolicy.Unit
	units, _ := p.Units()
	for _, u := range units {
		if u.Chunk < len(res.Compiled) && res.Compiled[u.Chunk] == 'c' {
			compiled = append(compiled, u)
		}
	}
	n.mu.Lock()
	seen := n.seen
	n.mu.Unlock()
	kept, err := n.store.Keep(compiled, nil, seen)
	if err != nil {
		n.t.Fatal(err)
	}
	n.mu.Lock()
	for _, u := range kept.Kept {
		if n.seen != nil {
			n.seen[u.Name] = u.Digest
		}
	}
	n.mu.Unlock()
	return res, kept
}

func (n *ns) val(code string) string {
	n.t.Helper()
	r, _ := n.run(code)
	if r.Outcome != "" && r.Outcome != "ok" {
		n.t.Fatalf("%s: %+v", code, r)
	}
	return r.Val
}

// A definition kept in one call is usable in the next; after a restart (session_start), and in a
// new extension's namespace at the same store path, the definition loads and the variable is
// gone.
func TestDefinitionPersistsIntoAFreshNamespace(t *testing.T) {
	store := newStore(t)
	a := newNS(t, store)
	_, kept := a.run("func up(s string) string { return strings.ToUpper(s) }\ntype Box struct{ N int }\nfunc (b Box) Twice() int { return b.N * 2 }\ny := 5")
	if got := strings.Join(kept.Lines(), "\n"); got != "kept: up, Box, Box.Twice" {
		t.Fatalf("%q", got)
	}
	if got := a.val(`fmt.Sprint(up("a"), Box{3}.Twice(), y)`); got != "A6 5" {
		t.Fatalf("%q", got)
	}
	a.sup.Kill()
	if got := a.val(`fmt.Sprint(up("b"), Box{4}.Twice())`); got != "B8" {
		t.Fatalf("after a restart: %q", got)
	}
	if r, _ := a.run("y"); r.Outcome != sandbox.OutcomeCompile {
		t.Fatalf("the variable survived a restart: %+v", r)
	}
	b := newNS(t, codestore.Store{Dir: store.Dir})
	if got := b.val(`up("c")`); !strings.Contains(got, "C") {
		t.Fatalf("a new namespace: %q", got)
	}
	if got := b.val(`kept()`); !strings.Contains(got, "up\tfunc\tkept") || !strings.Contains(got, "Box.Twice\tmethod\tkept") {
		t.Fatalf("kept(): %s", got)
	}
	if got := b.val(`kept("up")`); !strings.Contains(got, "strings.ToUpper(s)") {
		t.Fatalf("kept(up): %s", got)
	}
}

// forget removes a function from the store, and the stub it returns shadows it in the live
// namespace, so calls fail while the conversation's variables survive; after a restart the
// function is gone. A forgotten type needs a restart.
func TestForgetRemovesFromTheStoreAndTheLiveNamespace(t *testing.T) {
	store := newStore(t)
	a := newNS(t, store)
	a.run("func up(s string) string { return strings.ToUpper(s) }\ntype Box struct{ N int }\nv := 7")
	if got := a.val(`forget("up")`); !strings.Contains(got, "forgot up") {
		t.Fatalf("%q", got)
	}
	a.mu.Lock()
	stub := a.stub
	a.mu.Unlock()
	if stub == "" {
		t.Fatal("no stub for a function")
	}
	// The memory tool evaluates the stub in the live namespace when forget's call returns; it is
	// not kept.
	p, err := codepolicy.Check(codepolicy.Input{Code: stub})
	if err != nil {
		t.Fatal(err)
	}
	if r := a.sup.Run(context.Background(), sandbox.Request{Program: p}, a); r.Outcome != "" && r.Outcome != "ok" {
		t.Fatalf("stub: %+v", r)
	}
	if r, _ := a.run(`up("a")`); r.Outcome != sandbox.OutcomeRuntime || !strings.Contains(r.Err, "was forgotten") {
		t.Fatalf("the forgotten function answered: %+v", r)
	}
	if got := a.val("v"); got != "7" {
		t.Fatalf("variables lost: %q", got)
	}
	if l := load(t, store); len(names(l)) != 1 || names(l)[0] != "Box" {
		t.Fatalf("store %v", names(l))
	}
	a.sup.Kill()
	if r, _ := a.run(`up("a")`); r.Outcome != sandbox.OutcomeCompile {
		t.Fatalf("up survived a restart: %+v", r)
	}
	if got := a.val(`forget("Box")`); !strings.Contains(got, "restarts") {
		t.Fatalf("%q", got)
	}
}

// Reload is isolated: a stored unit that passes the load-time check but does not compile is a
// load error, leaves no callable symbol of its own or of a dependent, and the others load; a
// unit the load-time check refuses never reaches the child.
func TestReloadIsIsolated(t *testing.T) {
	store := newStore(t)
	units := []codestore.Unit{
		{Name: "good", Src: "func good() int { return 1 }"},
		{Name: "bad", Src: `func bad() int { return "not an int" }`},
		{Name: "usesBad", Src: "func usesBad() int { return bad() + 1 }"},
		{Name: "spawn", Src: "func spawn() { go func() {}() }"},
	}
	for i := range units {
		units[i].Digest = codestore.Digest(units[i].Src)
	}
	writeUnits(t, store, units)
	a := newNS(t, store)
	if got := a.val("good()"); got != "1" {
		t.Fatalf("%q", got)
	}
	for _, name := range []string{"bad", "usesBad", "spawn"} {
		if r, _ := a.run(name + "()"); r.Outcome != sandbox.OutcomeCompile {
			t.Errorf("%s is callable: %+v", name, r)
		}
	}
	st := a.sup.Status()
	if len(st.LoadErrors) != 2 || st.LoadErrors[0].Units[0] != "bad" || st.LoadErrors[1].Units[0] != "usesBad" || st.LoadErrors[1].Msg != "depends on bad" {
		t.Fatalf("load errors %+v", st.LoadErrors)
	}
	if r := refusedReason(load(t, store), "spawn"); !strings.Contains(r, "go statement") {
		t.Fatalf("spawn: %q", r)
	}
}
