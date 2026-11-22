package op_test

import (
	"testing"
	"time"

	"github.com/DocCyblade/abyssctl/internal/op"
)

func TestManualClock(t *testing.T) {
	start := time.Date(2026, 11, 21, 3, 0, 0, 0, time.UTC)
	c := op.NewManualClock(start)

	if got := c.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want the start time %v", got, start)
	}
	if got := c.Now(); !got.Equal(start) {
		t.Fatalf("Now() moved by itself: %v, want %v", got, start)
	}

	c.Advance(90 * time.Minute)
	want := time.Date(2026, 11, 21, 4, 30, 0, 0, time.UTC)
	if got := c.Now(); !got.Equal(want) {
		t.Fatalf("after Advance, Now() = %v, want %v", got, want)
	}

	set := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	c.Set(set)
	if got := c.Now(); !got.Equal(set) {
		t.Fatalf("after Set, Now() = %v, want %v", got, set)
	}

	var _ op.Clock = c
}
