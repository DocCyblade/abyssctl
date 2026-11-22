package op

import (
	"fmt"
	"sync"
	"time"
	"unicode/utf8"
)

// KV is one field of an Event.
type KV struct{ Key, Value string }

// F makes a field. The key is lowercase snake_case; the value is never a Secret.
func F(key, value string) KV { return KV{Key: key, Value: value} }

// Event is one progress line. Modules emit; only the CLI prints.
type Event struct {
	At     time.Time // set by the Emitter from its Clock, never by the module
	Step   string    // short verb for the line, e.g. "backup", "pull", "stop"
	Fields []KV      // in the order given
}

// Emitter is how a module reports progress.
type Emitter interface {
	Emit(step string, fields ...KV)
}

type emitter struct {
	clock Clock
	sink  func(Event)
}

func (e emitter) Emit(step string, fields ...KV) {
	e.sink(newEvent(e.clock, step, fields))
}

func newEvent(clock Clock, step string, fields []KV) Event {
	ev := Event{At: clock.Now(), Step: step}
	if len(fields) > 0 {
		ev.Fields = append([]KV(nil), fields...)
	}
	return ev
}

// NewEmitter returns the CLI's Emitter: it stamps each event from clock and
// hands it to sink, returning once sink has returned. It passes any step and
// key through unchecked.
func NewEmitter(clock Clock, sink func(Event)) Emitter { return emitter{clock: clock, sink: sink} }

type discard struct{}

func (discard) Emit(string, ...KV) {}

// Discard returns an Emitter that drops every event.
func Discard() Emitter { return discard{} }

// Recorder is an Emitter for tests in any package. It keeps every event and
// panics on a step or key that isn't lowercase snake_case ([a-z][a-z0-9_]*).
// It is safe for concurrent use.
type Recorder struct {
	clock  Clock
	mu     sync.Mutex
	events []Event
}

// NewRecorder returns an empty Recorder that stamps events from clock.
func NewRecorder(clock Clock) *Recorder { return &Recorder{clock: clock} }

// Emit records the event, or panics if the step or a key breaks the rule.
func (r *Recorder) Emit(step string, fields ...KV) {
	if !snakeCase(step) {
		panic(fmt.Sprintf("op.Recorder: step %q is not lowercase snake_case", step))
	}
	for _, f := range fields {
		if !snakeCase(f.Key) {
			panic(fmt.Sprintf("op.Recorder: key %q of step %q is not lowercase snake_case", f.Key, step))
		}
	}
	ev := newEvent(r.clock, step, fields)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

// Events returns a copy of the events recorded so far, in order.
func (r *Recorder) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}

// snakeCase reports whether s matches [a-z][a-z0-9_]*.
func snakeCase(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// Mask hides a Secret: "…" and its last four characters, or "…" alone if it is
// shorter than eight characters.
func Mask(secret string) string {
	if utf8.RuneCountInString(secret) < 8 {
		return "…"
	}
	r := []rune(secret)
	return "…" + string(r[len(r)-4:])
}
