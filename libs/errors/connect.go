package errors

import (
	stderrors "errors"
	"fmt"

	"connectrpc.com/connect"
)

// Connect integration. See ADR-0005.
//
// A service handler returns *Error (or any error); the transport layer uses
// ToConnect to convert it to *connect.Error. HTTP/JSON and gRPC clients
// observe the same Code; reason/domain travel in the message text.
//
// Use ToConnect at handler returns; use FromConnect in clients.

// CodeOK is the success code. connect.Code has no exported zero-name, so we
// surface one for symmetry with the rest of the API.
const CodeOK = connect.Code(0)

// ToConnect converts a generic error into a *connect.Error suitable for
// returning from a handler. *Error values are mapped via their Kind; other
// errors become Internal.
func ToConnect(err error) error {
	if err == nil {
		return nil
	}
	e := Wrap(err)
	return connect.NewError(e.Code(), fmt.Errorf("%s/%s: %s", e.Domain, e.Reason, e.Message))
}

// FromConnect converts a *connect.Error received by a client back into *Error,
// preserving the Code.
func FromConnect(err error) *Error {
	if err == nil {
		return nil
	}
	var ce *connect.Error
	if stderrors.As(err, &ce) {
		return New(codeToKind(ce.Code()), "vibesync", "REMOTE", ce.Message())
	}
	return Wrap(err)
}

func codeToKind(c connect.Code) Kind {
	switch c {
	case connect.CodeCanceled:
		return KindCancelled
	case connect.CodeUnknown:
		return KindUnknown
	case connect.CodeInvalidArgument:
		return KindInvalidArgument
	case connect.CodeDeadlineExceeded:
		return KindDeadlineExceeded
	case connect.CodeNotFound:
		return KindNotFound
	case connect.CodeAlreadyExists:
		return KindAlreadyExists
	case connect.CodePermissionDenied:
		return KindPermissionDenied
	case connect.CodeResourceExhausted:
		return KindResourceExhausted
	case connect.CodeFailedPrecondition:
		return KindFailedPrecondition
	case connect.CodeAborted:
		return KindAborted
	case connect.CodeOutOfRange:
		return KindOutOfRange
	case connect.CodeUnimplemented:
		return KindUnimplemented
	case connect.CodeInternal:
		return KindInternal
	case connect.CodeUnavailable:
		return KindUnavailable
	case connect.CodeUnauthenticated:
		return KindUnauthenticated
	default:
		return KindInternal
	}
}
