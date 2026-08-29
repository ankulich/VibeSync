// Package errors defines VibeSync's typed domain error model.
//
// All service code returns *Error values, never bare fmt.Errorf. Each *Error
// carries:
//
//   - a stable Reason string (e.g. "ROOM_FULL") that travels to clients;
//   - a Domain (e.g. "vibesync.room") used to namespace reasons;
//   - a canonical gRPC/Connect code derived from a Kind;
//   - optional structured metadata for debugging and dashboards;
//   - an optional wrapped cause for lossless chaining.
//
// Connect serializes *Error to google.rpc.Status with an ErrorInfo detail,
// and HTTP/JSON clients see the same reason/domain pair. See ADR-0005.
//
// The package also provides sentinel constructors for the common cases
// (NotFound, AlreadyExists, InvalidArgument, Unauthenticated, PermissionDenied,
// Conflict, FailedPrecondition, Internal) so call sites read naturally:
//
//	return nil, errors.NotFound("room", id).WithMeta("room_id", id)
//
// Repeated wraps with fmt.Errorf("%w", err) preserve the original *Error.
package errors

import (
	stderrors "errors"
	"fmt"

	"connectrpc.com/connect"
)

// Code is the canonical status code shared across gRPC, Connect, and HTTP.
// It is a thin alias to connect.Code so callers can compare directly:
//
//	if errors.Is(err, codes.NotFound) { ... }
//
// We re-export rather than alias to google.golang.org/grpc/codes to keep the
// dependency surface on Connect, which is our transport layer (ADR-0003).
type Code = connect.Code

// Kind classifies an error for code-mapping purposes. Internal kinds map 1:1
// to connect.Code values; this indirection lets us evolve the mapping without
// churn at call sites.
type Kind int

// Kind enumerates error classifications. Each value maps 1:1 to a Connect
// code via Kind.code(). See ADR-0005.
const (
	// KindOther is the zero value and maps to Internal. Always prefer an
	// explicit kind at construction sites.
	KindOther Kind = iota
	// KindCancelled maps to connect.CodeCanceled.
	KindCancelled
	// KindUnknown maps to connect.CodeUnknown.
	KindUnknown
	// KindInvalidArgument maps to connect.CodeInvalidArgument.
	KindInvalidArgument
	// KindDeadlineExceeded maps to connect.CodeDeadlineExceeded.
	KindDeadlineExceeded
	// KindNotFound maps to connect.CodeNotFound.
	KindNotFound
	// KindAlreadyExists maps to connect.CodeAlreadyExists.
	KindAlreadyExists
	// KindPermissionDenied maps to connect.CodePermissionDenied.
	KindPermissionDenied
	// KindResourceExhausted maps to connect.CodeResourceExhausted.
	KindResourceExhausted
	// KindFailedPrecondition maps to connect.CodeFailedPrecondition.
	KindFailedPrecondition
	// KindAborted maps to connect.CodeAborted. Used for conflicts/retries.
	KindAborted
	// KindOutOfRange maps to connect.CodeOutOfRange.
	KindOutOfRange
	// KindUnimplemented maps to connect.CodeUnimplemented.
	KindUnimplemented
	// KindInternal maps to connect.CodeInternal.
	KindInternal
	// KindUnavailable maps to connect.CodeUnavailable.
	KindUnavailable
	// KindUnauthenticated maps to connect.CodeUnauthenticated.
	KindUnauthenticated
)

// codeFor maps a Kind to a Connect Code. Kept as a function (not a map) so the
// compiler can inline it and the switch is exhaustive-checked by govet's
// nilness/switch analysis in newer Go versions.
func (k Kind) code() Code {
	switch k {
	case KindCancelled:
		return connect.CodeCanceled
	case KindUnknown:
		return connect.CodeUnknown
	case KindInvalidArgument:
		return connect.CodeInvalidArgument
	case KindDeadlineExceeded:
		return connect.CodeDeadlineExceeded
	case KindNotFound:
		return connect.CodeNotFound
	case KindAlreadyExists:
		return connect.CodeAlreadyExists
	case KindPermissionDenied:
		return connect.CodePermissionDenied
	case KindResourceExhausted:
		return connect.CodeResourceExhausted
	case KindFailedPrecondition:
		return connect.CodeFailedPrecondition
	case KindAborted:
		return connect.CodeAborted
	case KindOutOfRange:
		return connect.CodeOutOfRange
	case KindUnimplemented:
		return connect.CodeUnimplemented
	case KindInternal:
		return connect.CodeInternal
	case KindUnavailable:
		return connect.CodeUnavailable
	case KindUnauthenticated:
		return connect.CodeUnauthenticated
	default:
		return connect.CodeInternal
	}
}

// Error is the canonical error type returned by all service code.
//
// Construct via the New constructor or the typed helpers (NotFound, ...). Do
// not build a literal &Error{} — fields have invariants (Reason non-empty,
// Domain non-empty for non-internal kinds).
type Error struct {
	// Kind drives the Connect/gRPC code mapping.
	Kind Kind
	// Reason is a stable SCREAMING_SNAKE code surfaced to clients. Required.
	Reason string
	// Domain namespaces reasons; conventionally "vibesync.<service>". Required.
	Domain string
	// Message is a human-readable description. May be empty; callers should
	// prefer Reason for branching.
	Message string
	// Meta carries structured debugging data. Optional.
	Meta map[string]string
	// Cause is the wrapped error, if any. Preserved by errors.Is/As.
	Cause error
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	msg := e.Message
	if msg == "" {
		msg = e.Reason
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s/%s: %s: %v", e.Domain, e.Reason, msg, e.Cause)
	}
	return fmt.Sprintf("%s/%s: %s", e.Domain, e.Reason, msg)
}

// Unwrap returns the wrapped cause, enabling errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Cause }

// WithMeta returns a shallow copy of e with the given key/value merged into
// Meta. The original e is left untouched. Use this at the boundary where a
// call site wants to enrich an upstream error with local context.
func (e *Error) WithMeta(k, v string) *Error {
	cp := *e
	if cp.Meta == nil {
		cp.Meta = map[string]string{k: v}
	} else {
		cp.Meta = cloneMeta(cp.Meta)
		cp.Meta[k] = v
	}
	return &cp
}

// WithCause returns a shallow copy of e wrapping cause. Used when re-throwing
// an upstream error with a domain reason attached.
func (e *Error) WithCause(cause error) *Error {
	cp := *e
	cp.Cause = cause
	return &cp
}

// Code returns the Connect/gRPC code for this error.
func (e *Error) Code() Code {
	if e == nil {
		return CodeOK
	}
	return e.Kind.code()
}

// New constructs an *Error with full field control. Prefer the typed helpers
// (NotFound, etc.) for readability at call sites.
func New(kind Kind, domain, reason, message string) *Error {
	if reason == "" {
		reason = "UNSPECIFIED"
	}
	if domain == "" {
		domain = "vibesync"
	}
	return &Error{
		Kind:    kind,
		Reason:  reason,
		Domain:  domain,
		Message: message,
	}
}

// --- Typed constructors --------------------------------------------------
//
// Each helper fixes a Kind so call sites cannot accidentally misclassify.
// The `entity` argument is interpolated into the default message for logs and
// is included as Meta under "entity".

func invalidArgument(domain, reason, msg string) *Error {
	return New(KindInvalidArgument, domain, reason, msg)
}

// NotFound reports that a named entity was missing. The id is included as
// Meta for tracing.
func NotFound(entity, id string) *Error {
	return New(KindNotFound, domainFor(entity), "NOT_FOUND",
		fmt.Sprintf("%s not found: %s", entity, id)).
		WithMeta("entity", entity).
		WithMeta("id", id)
}

// AlreadyExists reports a uniqueness violation.
func AlreadyExists(entity, id string) *Error {
	return New(KindAlreadyExists, domainFor(entity), "ALREADY_EXISTS",
		fmt.Sprintf("%s already exists: %s", entity, id)).
		WithMeta("entity", entity).
		WithMeta("id", id)
}

// InvalidArgument reports client-supplied input that failed validation.
func InvalidArgument(reason, msg string) *Error {
	return invalidArgument("vibesync", reason, msg)
}

// InvalidArgumentFor is InvalidArgument scoped to a domain.
func InvalidArgumentFor(domain, reason, msg string) *Error {
	return invalidArgument(domain, reason, msg)
}

// PermissionDenied reports an authenticated subject lacking rights.
func PermissionDenied(action, resource string) *Error {
	return New(KindPermissionDenied, "vibesync.rbac", "PERMISSION_DENIED",
		fmt.Sprintf("not allowed to %s on %s", action, resource)).
		WithMeta("action", action).
		WithMeta("resource", resource)
}

// Unauthenticated reports a missing or invalid credential.
func Unauthenticated(reason string) *Error {
	return New(KindUnauthenticated, "vibesync.auth", reason,
		"authentication required")
}

// FailedPrecondition reports an action that cannot run in the current state
// (e.g. pausing an already-ended track). Distinct from InvalidArgument: the
// request was syntactically valid but the system state forbids it.
func FailedPrecondition(domain, reason, msg string) *Error {
	return New(KindFailedPrecondition, domain, reason, msg)
}

// Conflict reports a concurrent-modification or version conflict. Maps to
// Aborted so clients can retry.
func Conflict(domain, reason, msg string) *Error {
	return New(KindAborted, domain, reason, msg)
}

// ResourceExhausted reports a quota or capacity limit being hit.
func ResourceExhausted(domain, reason, msg string) *Error {
	return New(KindResourceExhausted, domain, reason, msg)
}

// Internal reports an unexpected server-side condition. Message is kept out of
// client responses by the transport layer; only Reason travels.
func Internal(reason, msg string) *Error {
	return New(KindInternal, "vibesync", reason, msg)
}

// Wrap converts an arbitrary error into an *Error if it isn't one already,
// classifying by default as Internal. Use at transport boundaries.
func Wrap(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if stderrors.As(err, &e) {
		return e
	}
	return Internal("INTERNAL", err.Error()).WithCause(err)
}

// --- standard library interop -------------------------------------------

// Is makes *Error comparable with sentinel *Errors. Comparison is by Kind
// and Reason (Domain wildcards when empty on the target).
//
// To compare against a Connect code, use e.Code() == connect.CodeNotFound
// directly. We deliberately do NOT implement errors.Is(err, someCode): the
// standard library requires the target to implement error, and connect.Code
// is a uint32 alias.
func (e *Error) Is(target error) bool {
	var t *Error
	if stderrors.As(target, &t) {
		return e != nil && t != nil &&
			e.Kind == t.Kind &&
			e.Reason == t.Reason &&
			(t.Domain == "" || e.Domain == t.Domain)
	}
	return false
}

func cloneMeta(m map[string]string) map[string]string {
	out := make(map[string]string, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// domainFor derives a domain from an entity noun. Conventionally an entity
// "room" maps to "vibesync.room". This keeps NotFound/AlreadyExists ergonomic
// while still producing a namespaced reason.
func domainFor(entity string) string {
	if entity == "" {
		return "vibesync"
	}
	return "vibesync." + entity
}
