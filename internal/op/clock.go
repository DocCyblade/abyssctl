// Package op is the contract between modules and the CLI.
package op

import "time"

// Clock is the one source of the time for production code.
type Clock interface{ Now() time.Time }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// SystemClock returns the real clock.
func SystemClock() Clock { return systemClock{} }
