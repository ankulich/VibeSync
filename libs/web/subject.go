// Subject-header helpers shared by every service that reads caller identity
// from the X-Vibesync-User-Id / X-Vibesync-System-Role headers (see the
// architecture review: these headers are trusted until the JWT interceptor
// lands — this file only normalizes their parsing).

package web

import (
	"strconv"

	commonv1 "vibesync/gen/go/vibesync/common/v1"
)

// ParseSystemRole decodes an X-Vibesync-System-Role header value into a
// SystemRole. The value arrives in one of three spellings depending on the
// client's codegen:
//
//   - the full proto enum name ("SYSTEM_ROLE_USER") — Go clients and the
//     future API gateway (protoc-gen-go's Enum_value map keys)
//   - the short name ("USER") — the web frontend: protobuf-es generates
//     TypeScript enums whose reverse mapping (SystemRole[2]) yields the
//     short name
//   - the numeric wire value ("2")
//
// Anything else (including the empty string) parses as UNSPECIFIED, which
// every authorization check treats as "no rights" — so a spelling mismatch
// fails closed, not open.
func ParseSystemRole(s string) commonv1.SystemRole {
	if s == "" {
		return commonv1.SystemRole_SYSTEM_ROLE_UNSPECIFIED
	}
	if v, ok := commonv1.SystemRole_value[s]; ok {
		return commonv1.SystemRole(v)
	}
	if v, err := strconv.Atoi(s); err == nil {
		if _, ok := commonv1.SystemRole_name[int32(v)]; ok {
			return commonv1.SystemRole(v)
		}
	}
	if v, ok := commonv1.SystemRole_value["SYSTEM_ROLE_"+s]; ok {
		return commonv1.SystemRole(v)
	}
	return commonv1.SystemRole_SYSTEM_ROLE_UNSPECIFIED
}
