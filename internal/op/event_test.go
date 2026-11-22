package op_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/DocCyblade/abyssctl/internal/op"
)

func TestEmitter_StampsAndOrders(t *testing.T) {
	t0 := time.Date(2026, 11, 21, 3, 0, 0, 0, time.UTC)
	t1 := t0.Add(2 * time.Second)

	emitters := map[string]func(*op.ManualClock) (op.Emitter, func() []op.Event){
		"NewEmitter": func(c *op.ManualClock) (op.Emitter, func() []op.Event) {
			var got []op.Event
			return op.NewEmitter(c, func(e op.Event) { got = append(got, e) }), func() []op.Event { return got }
		},
		"Recorder": func(c *op.ManualClock) (op.Emitter, func() []op.Event) {
			r := op.NewRecorder(c)
			return r, r.Events
		},
	}
	for name, mk := range emitters {
		t.Run(name, func(t *testing.T) {
			clock := op.NewManualClock(t0)
			em, events := mk(clock)

			em.Emit("backup", op.F("instance", "kids"), op.F("size", "12MB"), op.F("archive", "a.tar"))
			if n := len(events()); n != 1 {
				t.Fatalf("after one Emit the sink has %d events, want 1 (Emit is synchronous)", n)
			}
			clock.Set(t1)
			em.Emit("stop")

			want := []op.Event{
				{At: t0, Step: "backup", Fields: []op.KV{{"instance", "kids"}, {"size", "12MB"}, {"archive", "a.tar"}}},
				{At: t1, Step: "stop"},
			}
			got := events()
			if len(got) != len(want) {
				t.Fatalf("got %d events, want %d: %+v", len(got), len(want), got)
			}
			for i := range want {
				if !got[i].At.Equal(want[i].At) || got[i].Step != want[i].Step ||
					len(got[i].Fields) != len(want[i].Fields) ||
					(len(want[i].Fields) > 0 && !reflect.DeepEqual(got[i].Fields, want[i].Fields)) {
					t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
				}
			}
		})
	}
}

func TestEmitter_PassesBadKeysThrough(t *testing.T) {
	var got []op.Event
	em := op.NewEmitter(op.NewManualClock(time.Time{}), func(e op.Event) { got = append(got, e) })
	em.Emit("Bad Step", op.F("Bad-Key", "v"))
	if len(got) != 1 || got[0].Step != "Bad Step" || got[0].Fields[0].Key != "Bad-Key" {
		t.Fatalf("the production Emitter changed or dropped the event: %+v", got)
	}
}

func TestDiscard(t *testing.T) {
	op.Discard().Emit("backup", op.F("instance", "kids"))
}

func TestRecorder_RejectsBadKey(t *testing.T) {
	tests := []struct {
		name   string
		step   string
		fields []op.KV
		panics bool
	}{
		{"good", "health_check", []op.KV{op.F("instance", "kids"), op.F("try2", "x")}, false},
		{"empty step", "", nil, true},
		{"upper-case step", "Backup", nil, true},
		{"step with a space", "back up", nil, true},
		{"step with a hyphen", "back-up", nil, true},
		{"step starting with a digit", "2fa", nil, true},
		{"step starting with an underscore", "_backup", nil, true},
		{"empty key", "backup", []op.KV{op.F("", "v")}, true},
		{"upper-case key", "backup", []op.KV{op.F("Instance", "v")}, true},
		{"camel-case key", "backup", []op.KV{op.F("instanceName", "v")}, true},
		{"key with a dot", "backup", []op.KV{op.F("ok", "v"), op.F("a.b", "v")}, true},
		{"non-ASCII key", "backup", []op.KV{op.F("café", "v")}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := op.NewRecorder(op.NewManualClock(time.Time{}))
			defer func() {
				if p := recover(); (p != nil) != tt.panics {
					t.Errorf("Emit(%q, %v) panic = %v, want panic %v", tt.step, tt.fields, p, tt.panics)
				}
			}()
			r.Emit(tt.step, tt.fields...)
		})
	}
}

func TestMask(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "…"},
		{"abc", "…"},
		{"abcdefg", "…"},
		{"abcdefgh", "…efgh"},
		{"uQiRzpo4DXghDmr9QzzfQu27cmVRsG", "…VRsG"},
		{"ééééééé", "…"},
		{"ééééabcé", "…abcé"},
	}
	for _, tt := range tests {
		if got := op.Mask(tt.in); got != tt.want {
			t.Errorf("Mask(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
