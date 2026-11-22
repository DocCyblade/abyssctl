package op_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/DocCyblade/abyssctl/internal/op"
)

func TestExitCode(t *testing.T) {
	plain := errors.New("docker: connection refused")
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"plain error", plain, 1},
		{"lock timeout", fmt.Errorf("lock kids: %w", context.DeadlineExceeded), 1},
		{"refused", op.Refused("not root"), 2},
		{"rolled back", op.RolledBack("the upgrade failed"), 3},
		{"needs the Operator", op.NeedsOperator("the restore was interrupted", "abyssctl restore kids"), 4},
		{"refused with a cause", op.Refused("cannot read the env file").WithCause(plain), 2},
		{"refused, wrapped twice", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", op.Refused("bad name"))), 2},
		{"rolled back, wrapped", fmt.Errorf("upgrade kids: %w", op.RolledBack("health failed")), 3},
		{"needs the Operator, wrapped", fmt.Errorf("x: %w", op.NeedsOperator("rollback failed", "abyssctl status kids")), 4},
		{"outermost wins: refused around needs the Operator", op.Refused("outer").WithCause(op.NeedsOperator("inner", "abyssctl status")), 2},
		{"outermost wins: needs the Operator around refused", op.NeedsOperator("outer", "abyssctl status").WithCause(op.Refused("inner")), 4},
		{"outermost wins: rolled back around refused, wrapped", fmt.Errorf("w: %w", op.RolledBack("outer").WithCause(op.Refused("inner"))), 3},
		{"bare refused sentinel", fmt.Errorf("w: %w", op.ErrRefused), 2},
		{"bare rolled back sentinel", fmt.Errorf("w: %w", op.ErrRolledBack), 3},
		{"bare needs the Operator sentinel", fmt.Errorf("w: %w", op.ErrNeedsOperator), 4},
		{"bare sentinels: 4 before 3 before 2", errors.Join(op.ErrRefused, op.ErrRolledBack, op.ErrNeedsOperator), 4},
		{"bare sentinels: 3 before 2", errors.Join(op.ErrRefused, op.ErrRolledBack), 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := op.ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestHint(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"plain error", errors.New("boom"), ""},
		{"no hint", op.Refused("bad name"), ""},
		{"with hint", op.Refused("not running").WithHint("abyssctl start kids"), "abyssctl start kids"},
		{"needs the Operator", op.NeedsOperator("rollback failed", "abyssctl status kids"), "abyssctl status kids"},
		{"wrapped", fmt.Errorf("w: %w", op.RolledBack("health failed").WithHint("abyssctl logs kids")), "abyssctl logs kids"},
		{"outermost wins", op.Refused("outer").WithHint("abyssctl outer").WithCause(op.NeedsOperator("inner", "abyssctl inner")), "abyssctl outer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := op.Hint(tt.err); got != tt.want {
				t.Errorf("Hint(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestNeedsOperator_RequiresHint(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NeedsOperator with an empty hint did not panic")
		}
	}()
	_ = op.NeedsOperator("the restore was interrupted", "")
}

func TestError_MessageAndUnwrap(t *testing.T) {
	cause := errors.New("disk full")
	tests := []struct {
		name    string
		err     *op.Error
		wantMsg string
		class   error
	}{
		{"no cause", op.Refused("bad name"), "bad name", op.ErrRefused},
		{"with cause", op.RolledBack("health failed").WithCause(cause), "health failed: disk full", op.ErrRolledBack},
		{"hint not in message", op.NeedsOperator("rollback failed", "abyssctl status kids"), "rollback failed", op.ErrNeedsOperator},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.wantMsg {
				t.Errorf("Error() = %q, want %q", got, tt.wantMsg)
			}
			if !errors.Is(tt.err, tt.class) {
				t.Errorf("errors.Is(err, %v) = false", tt.class)
			}
			if tt.err.Cause != nil && !errors.Is(tt.err, tt.err.Cause) {
				t.Errorf("errors.Is(err, cause) = false")
			}
		})
	}
}
