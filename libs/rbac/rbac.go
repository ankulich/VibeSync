// Package rbac evaluates role-based access for VibeSync.
//
// Two role dimensions exist (ADR RBAC):
//
//  1. System roles (global): Administrator > Moderator > User > Guest.
//  2. Room roles (scoped to a room): Owner > Moderator > Member > Guest.
//
// A permission check takes (subject roles, action, resource) and returns
// allow/deny. The policy is data-driven: a static policy table covers the
// common cases, and rooms may attach overrides (e.g. "members cannot skip").
//
// This package ships the evaluator and the default policy. Persistence
// (room role membership, override storage) lands with the Room Service
// (Phase 6), which owns role assignment.
package rbac

import (
	commonv1 "vibesync/gen/go/vibesync/common/v1"
)

// SystemRole and RoomRole re-export the generated enums so callers don't
// import gen/go transitively through multiple paths.
type SystemRole = commonv1.SystemRole

// RoomRole re-exports the generated room-scoped role enum.
type RoomRole = commonv1.RoomRole

// Action is a stable verb naming what the subject wants to do.
type Action string

const (
	// ActionCreateRoom authorizes creating a new room.
	ActionCreateRoom Action = "room.create"
	// ActionDeleteRoom authorizes deleting a room.
	ActionDeleteRoom Action = "room.delete"
	// ActionInviteUser authorizes inviting a user to a room.
	ActionInviteUser Action = "room.invite"
	// ActionKickUser authorizes removing a user from a room.
	ActionKickUser Action = "room.kick"
	// ActionPromoteUser authorizes changing a room member's role.
	ActionPromoteUser Action = "room.promote"
	// ActionCommandSync authorizes issuing a playback command to the Sync Service.
	ActionCommandSync Action = "sync.command"
	// ActionManageMedia authorizes modifying a room's media queue.
	ActionManageMedia Action = "media.manage"
	// ActionUploadMedia authorizes uploading media to the Storage Service.
	ActionUploadMedia Action = "media.upload"
	// ActionRotateKeys authorizes rotating the Auth Service signing keys.
	ActionRotateKeys Action = "auth.rotate_keys"
	// ActionViewUsers authorizes listing all users (administrator-only).
	ActionViewUsers Action = "user.view_all"
)

// Subject is the actor being authorized.
type Subject struct {
	UserID     string
	SystemRole SystemRole
	// RoomRole is the subject's role in the room the action targets. Zero
	// value when the action is not room-scoped.
	RoomRole RoomRole
	RoomID   string
}

// Resource identifies what is being acted upon. For room-scoped actions the
// RoomID must be populated.
type Resource struct {
	Kind   string // "room", "user", "media", ...
	RoomID string
	ID     string
}

// Policy is the contract an evaluator consults. The default implementation
// below covers the static rules; rooms may register overrides.
type Policy interface {
	// Allow reports whether subject may perform action on resource.
	Allow(subject Subject, action Action, resource Resource) bool
}

// Evaluator is the front door for authorization checks.
type Evaluator struct {
	policy Policy
}

// New returns an Evaluator backed by the given policy. Use Default() for the
// standard static policy.
func New(p Policy) *Evaluator { return &Evaluator{policy: p} }

// Allow delegates to the policy. Empty/UNSPECIFIED roles always deny.
func (e *Evaluator) Allow(s Subject, a Action, r Resource) bool {
	if s.SystemRole == commonv1.SystemRole_SYSTEM_ROLE_UNSPECIFIED &&
		s.RoomRole == commonv1.RoomRole_ROOM_ROLE_UNSPECIFIED {
		return false
	}
	return e.policy.Allow(s, a, r)
}

// DefaultPolicy is the system-wide static rule set. See docs/rbac.md.
type DefaultPolicy struct{}

// Default returns the standard policy. It has no state.
func Default() Policy { return DefaultPolicy{} }

// Allow implements Policy. Administrators bypass all checks. Otherwise the
// matrix is evaluated per action.
func (DefaultPolicy) Allow(s Subject, a Action, _ Resource) bool {
	if s.SystemRole >= commonv1.SystemRole_SYSTEM_ROLE_ADMINISTRATOR {
		return true
	}
	switch a {
	case ActionCreateRoom, ActionUploadMedia:
		// Any authenticated user (not Guest) may create rooms and upload.
		return s.SystemRole >= commonv1.SystemRole_SYSTEM_ROLE_USER
	case ActionCommandSync:
		// Host or moderator in the room may issue sync commands.
		return s.RoomRole >= commonv1.RoomRole_ROOM_ROLE_MODERATOR ||
			s.RoomRole == commonv1.RoomRole_ROOM_ROLE_OWNER
	case ActionDeleteRoom, ActionKickUser, ActionPromoteUser, ActionInviteUser, ActionManageMedia:
		return s.RoomRole >= commonv1.RoomRole_ROOM_ROLE_OWNER ||
			s.RoomRole == commonv1.RoomRole_ROOM_ROLE_MODERATOR && a != ActionDeleteRoom
	case ActionRotateKeys, ActionViewUsers:
		return s.SystemRole >= commonv1.SystemRole_SYSTEM_ROLE_ADMINISTRATOR
	default:
		return false
	}
}
