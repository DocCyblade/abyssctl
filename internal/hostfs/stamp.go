package hostfs

import (
	"fmt"
	"time"
)

// stampLayout is ISO 8601 basic format in UTC. It holds no "-" or ":", and sorts as text in time order.
const stampLayout = "20060102T150405Z"

// Stamp gives t in UTC, ISO 8601 basic format, e.g. "20261117T080004Z", for machine-facing times in file names.
// Sub-second detail is dropped.
func Stamp(t time.Time) string { return t.UTC().Format(stampLayout) }

// ParseStamp reverses Stamp, giving a time in UTC.
func ParseStamp(s string) (time.Time, error) {
	t, err := time.Parse(stampLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a stamp (YYYYMMDDTHHMMSSZ): %w", s, err)
	}
	return t.UTC(), nil
}
