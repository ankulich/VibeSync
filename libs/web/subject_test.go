package web

import (
	"testing"

	commonv1 "vibesync/gen/go/vibesync/common/v1"
)

// The X-Vibesync-System-Role header is spelled differently by different
// codegens (full proto name from Go, short name from protobuf-es/TS, or the
// raw wire number). ParseSystemRole must accept all three and fail closed
// on garbage.
func TestParseSystemRole(t *testing.T) {
	cases := []struct {
		in   string
		want commonv1.SystemRole
	}{
		{"", commonv1.SystemRole_SYSTEM_ROLE_UNSPECIFIED},
		{"SYSTEM_ROLE_USER", commonv1.SystemRole_SYSTEM_ROLE_USER},
		{"SYSTEM_ROLE_ADMINISTRATOR", commonv1.SystemRole_SYSTEM_ROLE_ADMINISTRATOR},
		{"USER", commonv1.SystemRole_SYSTEM_ROLE_USER}, // protobuf-es short name (the web frontend)
		{"ADMINISTRATOR", commonv1.SystemRole_SYSTEM_ROLE_ADMINISTRATOR},
		{"2", commonv1.SystemRole_SYSTEM_ROLE_USER}, // numeric wire value
		{"4", commonv1.SystemRole_SYSTEM_ROLE_ADMINISTRATOR},
		{"garbage", commonv1.SystemRole_SYSTEM_ROLE_UNSPECIFIED}, // fail closed
		{"99", commonv1.SystemRole_SYSTEM_ROLE_UNSPECIFIED},      // out-of-range number
		{"-1", commonv1.SystemRole_SYSTEM_ROLE_UNSPECIFIED},
	}
	for _, tc := range cases {
		if got := ParseSystemRole(tc.in); got != tc.want {
			t.Errorf("ParseSystemRole(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
