package domain

import (
	roomv1 "vibesync/gen/go/vibesync/room/v1"
)

// Permissions is a member's set of owner-granted control grants (ADR-0017),
// stored as a bitmask. The room owner — and the acting host — implicitly
// hold every permission; a bitmask of 0 means "no extra grants".
type Permissions uint16

// Individual permission bits. Values mirror the RoomPermission proto enum
// so the mapping is a plain shift (bit = proto value - 1).
const (
	PermSeek Permissions = 1 << iota // ROOM_PERMISSION_SEEK: rewind the room clock
	PermPausePlay                    // ROOM_PERMISSION_PAUSE_PLAY: pause / resume
	PermSwitchQueue                  // ROOM_PERMISSION_SWITCH_QUEUE: load / next / previous
	PermAddQueue                     // ROOM_PERMISSION_ADD_QUEUE: enqueue media
	PermRemoveQueue                  // ROOM_PERMISSION_REMOVE_QUEUE: dequeue media
)

// permByProto maps each proto RoomPermission value to its bit. UNSPECIFIED
// and any unknown value map to no bit.
var permByProto = map[int32]Permissions{
	int32(roomv1.RoomPermission_ROOM_PERMISSION_SEEK):         PermSeek,
	int32(roomv1.RoomPermission_ROOM_PERMISSION_PAUSE_PLAY):   PermPausePlay,
	int32(roomv1.RoomPermission_ROOM_PERMISSION_SWITCH_QUEUE): PermSwitchQueue,
	int32(roomv1.RoomPermission_ROOM_PERMISSION_ADD_QUEUE):    PermAddQueue,
	int32(roomv1.RoomPermission_ROOM_PERMISSION_REMOVE_QUEUE): PermRemoveQueue,
}

// PermissionsFromProto converts proto RoomPermission values into the
// bitmask, ignoring unknown values.
func PermissionsFromProto(perms []roomv1.RoomPermission) Permissions {
	var out Permissions
	for _, p := range perms {
		out |= permByProto[int32(p)]
	}
	return out
}

// ToProto converts the bitmask back to proto values, in enum order.
func (p Permissions) ToProto() []roomv1.RoomPermission {
	out := make([]roomv1.RoomPermission, 0, len(permByProto))
	for v := int32(1); v <= int32(len(permByProto)); v++ {
		if bit, ok := permByProto[v]; ok && p&bit != 0 {
			out = append(out, roomv1.RoomPermission(v))
		}
	}
	return out
}

// Has reports whether every bit of want is set.
func (p Permissions) Has(want Permissions) bool {
	return p&want == want
}
