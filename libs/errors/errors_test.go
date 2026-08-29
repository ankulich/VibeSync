package errors

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
)

func TestErrorFormatting(t *testing.T) {
	t.Parallel()
	e := New(KindNotFound, "vibesync.room", "ROOM_NOT_FOUND", "room 123 missing")
	if got := e.Error(); got != "vibesync.room/ROOM_NOT_FOUND: room 123 missing" {
		t.Fatalf("Error() = %q, want canonical form", got)
	}
}

func TestTypedHelpersSetKindAndReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  *Error
		kind Kind
		code Code
	}{
		{"not found", NotFound("room", "123"), KindNotFound, connect.CodeNotFound},
		{"already exists", AlreadyExists("user", "abc"), KindAlreadyExists, connect.CodeAlreadyExists},
		{"invalid", InvalidArgument("BAD_INPUT", "x"), KindInvalidArgument, connect.CodeInvalidArgument},
		{"permission", PermissionDenied("read", "room"), KindPermissionDenied, connect.CodePermissionDenied},
		{"unauth", Unauthenticated("NO_TOKEN"), KindUnauthenticated, connect.CodeUnauthenticated},
		{"failed pre", FailedPrecondition("vibesync.sync", "NOT_HOST", "x"), KindFailedPrecondition, connect.CodeFailedPrecondition},
		{"conflict", Conflict("vibesync.room", "CONCURRENT_EDIT", "x"), KindAborted, connect.CodeAborted},
		{"exhausted", ResourceExhausted("vibesync.room", "ROOM_FULL", "x"), KindResourceExhausted, connect.CodeResourceExhausted},
		{"internal", Internal("BOOM", "x"), KindInternal, connect.CodeInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.err.Kind != tc.kind {
				t.Errorf("Kind = %v, want %v", tc.err.Kind, tc.kind)
			}
			if tc.err.Code() != tc.code {
				t.Errorf("Code() = %v, want %v", tc.err.Code(), tc.code)
			}
			if tc.err.Reason == "" {
				t.Error("Reason must be non-empty")
			}
			if tc.err.Domain == "" {
				t.Error("Domain must be non-empty")
			}
		})
	}
}

func TestWithMetaDoesNotMutateOriginal(t *testing.T) {
	t.Parallel()
	orig := NotFound("room", "1")
	derived := orig.WithMeta("trace", "abc")
	if _, ok := orig.Meta["trace"]; ok {
		t.Error("WithMeta must not mutate the original Error")
	}
	if derived.Meta["trace"] != "abc" {
		t.Error("derived Error must carry the new Meta entry")
	}
	if derived.Meta["id"] != "1" {
		t.Error("derived Error must preserve original Meta")
	}
}

func TestWrapPreservesExistingError(t *testing.T) {
	t.Parallel()
	original := NotFound("room", "1")
	wrapped := Wrap(original)
	if wrapped != original {
		t.Error("Wrap on *Error must return the same pointer")
	}
}

func TestWrapClassifiesUnknownAsInternal(t *testing.T) {
	t.Parallel()
	wrapped := Wrap(errors.New("boom"))
	if wrapped.Kind != KindInternal {
		t.Errorf("Wrap of a plain error must be Internal, got %v", wrapped.Kind)
	}
	if wrapped.Unwrap() == nil || wrapped.Unwrap().Error() != "boom" {
		t.Errorf("Cause not preserved")
	}
}

func TestCodeComparison(t *testing.T) {
	t.Parallel()
	e := NotFound("room", "1")
	if e.Code() != connect.CodeNotFound {
		t.Error("Code() must report NotFound for NotFound errors")
	}
	if e.Code() == connect.CodePermissionDenied {
		t.Error("Code() must not match unrelated codes")
	}
}

func TestIsAgainstError(t *testing.T) {
	t.Parallel()
	e := NotFound("room", "1")
	sentinel := New(KindNotFound, "vibesync.room", "NOT_FOUND", "")
	if !errors.Is(e, sentinel) {
		t.Error("errors.Is must match same Kind+Reason")
	}
	other := New(KindInternal, "vibesync.room", "NOT_FOUND", "")
	if errors.Is(e, other) {
		t.Error("errors.Is must not match different Kind")
	}
}

func TestToConnectRoundTripPreservesCode(t *testing.T) {
	t.Parallel()
	e := NotFound("room", "1")
	ce := ToConnect(e)
	if ce == nil {
		t.Fatal("ToConnect returned nil for non-nil error")
	}
	back := FromConnect(ce)
	if back.Code() != e.Code() {
		t.Errorf("round-trip code = %v, want %v", back.Code(), e.Code())
	}
}

func TestNilErrorHasOKCode(t *testing.T) {
	t.Parallel()
	var e *Error
	if got := e.Code(); got != CodeOK {
		t.Errorf("nil.Code() = %v, want CodeOK", got)
	}
}

func TestNilErrorMethodSafe(t *testing.T) {
	t.Parallel()
	var e *Error
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil receiver panicked: %v", r)
		}
	}()
	_ = e.Error()
	_ = e.Is(nil)
}

func TestUnwrapChainsToCause(t *testing.T) {
	t.Parallel()
	root := errors.New("disk full")
	e := Internal("DISK_FULL", "").WithCause(root)
	if !errors.Is(e, root) {
		t.Error("errors.Is must traverse the Cause chain")
	}
}
