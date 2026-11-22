package op

import "errors"

// The three classes of typed error, and their exit codes.
var (
	ErrRefused       = errors.New("refused")            // exit 2
	ErrRolledBack    = errors.New("rolled back")        // exit 3
	ErrNeedsOperator = errors.New("needs the Operator") // exit 4
)

// Error is a typed error: a Class, a message for the Operator, an optional
// Hint and an optional Cause.
type Error struct {
	Class error  // ErrRefused, ErrRolledBack or ErrNeedsOperator
	Msg   string // one sentence for the Operator, no trailing period, never a Secret
	Hint  string // optional next step as a full command line, e.g. "abyssctl restart kids"
	Cause error  // optional underlying error
}

// Error gives Msg, then ": " and the Cause if one is set.
func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Msg
	}
	return e.Msg + ": " + e.Cause.Error()
}

// Unwrap returns the Class and the Cause, so errors.Is(err, ErrRefused) works.
func (e *Error) Unwrap() []error {
	if e.Cause == nil {
		return []error{e.Class}
	}
	return []error{e.Class, e.Cause}
}

// Refused reports that nothing was changed (exit 2). A module returns it only
// before it has changed anything.
func Refused(msg string) *Error { return &Error{Class: ErrRefused, Msg: msg} }

// RolledBack reports that an Upgrade failed and the Instance is back on its
// old version and data (exit 3).
func RolledBack(msg string) *Error { return &Error{Class: ErrRolledBack, Msg: msg} }

// NeedsOperator reports that the tool stopped and won't go on by itself
// (exit 4). The hint is required: NeedsOperator panics on an empty one.
func NeedsOperator(msg, hint string) *Error {
	if hint == "" {
		panic("op.NeedsOperator: the hint is required")
	}
	return &Error{Class: ErrNeedsOperator, Msg: msg, Hint: hint}
}

// WithHint returns a copy of e with its Hint set.
func (e *Error) WithHint(hint string) *Error {
	c := *e
	c.Hint = hint
	return &c
}

// WithCause returns a copy of e with its Cause set.
func (e *Error) WithCause(err error) *Error {
	c := *e
	c.Cause = err
	return &c
}

// ExitCode maps an error to the exit code: 0 for nil; the Class of the
// outermost *Error (2, 3 or 4); with no *Error in the chain, a bare sentinel
// checked in the order 4, 3, 2; and 1 for anything else.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var e *Error
	if errors.As(err, &e) {
		if code := classCode(e.Class); code != 0 {
			return code
		}
		return 1
	}
	for _, class := range []error{ErrNeedsOperator, ErrRolledBack, ErrRefused} {
		if errors.Is(err, class) {
			return classCode(class)
		}
	}
	return 1
}

func classCode(class error) int {
	switch class {
	case ErrRefused:
		return 2
	case ErrRolledBack:
		return 3
	case ErrNeedsOperator:
		return 4
	}
	return 0
}

// Hint returns the Hint of the outermost *Error in the chain, or "".
func Hint(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Hint
	}
	return ""
}
