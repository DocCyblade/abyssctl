package hostfs_test

import (
	"sort"
	"testing"
	"time"

	"github.com/DocCyblade/abyssctl/internal/hostfs"
)

func TestStamp(t *testing.T) {
	newYork := time.FixedZone("EST", -5*3600)
	kolkata := time.FixedZone("IST", 5*3600+30*60)

	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"utc", time.Date(2026, 11, 17, 8, 0, 4, 0, time.UTC), "20261117T080004Z"},
		{"behind_utc", time.Date(2026, 11, 17, 3, 0, 4, 0, newYork), "20261117T080004Z"},
		{"ahead_of_utc_previous_day", time.Date(2026, 11, 17, 2, 0, 0, 0, kolkata), "20261116T203000Z"},
		{"drops_subseconds", time.Date(2026, 1, 2, 3, 4, 5, 999_999_999, time.UTC), "20260102T030405Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hostfs.Stamp(c.in)
			if got != c.want {
				t.Fatalf("Stamp(%v) = %q, want %q", c.in, got, c.want)
			}
			back, err := hostfs.ParseStamp(got)
			if err != nil {
				t.Fatalf("ParseStamp(%q): %v", got, err)
			}
			if back.Location() != time.UTC {
				t.Errorf("ParseStamp(%q) location = %v, want UTC", got, back.Location())
			}
			if want := c.in.Truncate(time.Second); !back.Equal(want) {
				t.Errorf("ParseStamp(%q) = %v, want %v", got, back, want)
			}
		})
	}

	for _, bad := range []string{
		"", "2026-11-17T08:00:04Z", "20261117T080004", "20261117080004Z",
		"20261317T080004Z", "20261117T080004+0000",
	} {
		t.Run("rejects/"+bad, func(t *testing.T) {
			if _, err := hostfs.ParseStamp(bad); err == nil {
				t.Errorf("ParseStamp(%q) = nil error", bad)
			}
		})
	}

	t.Run("text_order_is_time_order", func(t *testing.T) {
		inTimeOrder := []time.Time{ // across zones and a year boundary
			time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC),
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 1, 1, 6, 0, 0, 0, kolkata), // 00:30 UTC
			time.Date(2026, 1, 1, 0, 0, 0, 0, newYork), // 05:00 UTC
			time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2026, 10, 1, 0, 0, 10, 0, time.UTC),
		}
		var stamps []string
		for i := len(inTimeOrder) - 1; i >= 0; i-- {
			stamps = append(stamps, hostfs.Stamp(inTimeOrder[i]))
		}
		sort.Strings(stamps)
		for i, s := range stamps {
			back, err := hostfs.ParseStamp(s)
			if err != nil {
				t.Fatalf("ParseStamp(%q): %v", s, err)
			}
			if !back.Equal(inTimeOrder[i]) {
				t.Errorf("sorted[%d] = %q (%v), want %v", i, s, back, inTimeOrder[i])
			}
		}
	})
}
