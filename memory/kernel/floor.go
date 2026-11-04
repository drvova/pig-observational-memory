package kernel

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// Lowering is one recorded decision to lower the floor: who asked, why, and when.
type Lowering struct {
	RequestedBy string `json:"requestedBy"`
	Reason      string `json:"reason"`
	Time        string `json:"time"`
	// Op is "lower" or "engage".
	Op string `json:"op"`
}

// The floor operations a Lowering records.
const (
	OpLower  = "lower"
	OpEngage = "engage"
)

// Floor is the kernel floor's state, after nodecode's engage-floor and lower-floor: engaged,
// Check refuses every definition that touches a kernel-owned name; lowered, it admits them.
// The model cannot lower it (no memory tool verb reaches it); only the user can, in
// configuration, and a lowering is recorded with who asked and why before it takes effect, so
// a lowering that cannot be recorded does not happen.
//
// A nil *Floor is engaged and records nothing.
type Floor struct {
	// Record appends a lowering or an engagement to a durable record. Lower refuses without one.
	Record func(Lowering) error
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	// changing serialises Lower and Engage, so a change is recorded and applied as one; mu
	// guards lowered alone, so Lowered and Check never wait on a record being written.
	changing sync.Mutex
	mu       sync.Mutex
	lowered  *Lowering
}

// Lower records who asked to lower the floor and why, then lowers it. Both are required, and
// the record is written first: an error leaves the floor engaged.
func (f *Floor) Lower(requestedBy, reason string) error {
	if f == nil {
		return errors.New("there is no floor to lower")
	}
	requestedBy, reason = strings.TrimSpace(requestedBy), strings.TrimSpace(reason)
	if requestedBy == "" || reason == "" {
		return errors.New("lowering the kernel floor requires who asked (requestedBy) and why (reason)")
	}
	if f.Record == nil {
		return errors.New("lowering the kernel floor requires a record to write it to; the floor stays engaged")
	}
	f.changing.Lock()
	defer f.changing.Unlock()
	l := Lowering{RequestedBy: requestedBy, Reason: reason, Time: f.now().UTC().Format(time.RFC3339Nano), Op: OpLower}
	if err := f.Record(l); err != nil {
		return err
	}
	f.mu.Lock()
	f.lowered = &l
	f.mu.Unlock()
	return nil
}

// Engage raises a lowered floor again, recording that it did when a record is set (the floor
// is engaged even when that record fails: engaging only narrows what is admitted).
func (f *Floor) Engage() error {
	if f == nil {
		return nil
	}
	f.changing.Lock()
	defer f.changing.Unlock()
	f.mu.Lock()
	wasLowered := f.lowered != nil
	f.lowered = nil
	f.mu.Unlock()
	if !wasLowered || f.Record == nil {
		return nil
	}
	return f.Record(Lowering{Time: f.now().UTC().Format(time.RFC3339Nano), Op: OpEngage})
}

// Lowered reports whether the floor is lowered, and the lowering that did it.
func (f *Floor) Lowered() (Lowering, bool) {
	if f == nil {
		return Lowering{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lowered == nil {
		return Lowering{}, false
	}
	return *f.lowered, true
}

// Check is the package Check while the floor is engaged; a lowered floor admits every
// definition. Even lowered, nothing in the layer can deactivate the memory tool or raise the
// room ceiling: those are code paths, not names a definition reaches.
func (f *Floor) Check(d Def) error {
	if _, lowered := f.Lowered(); lowered {
		return nil
	}
	return Check(d)
}

func (f *Floor) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}
