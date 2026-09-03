import { RoomRole } from '../gen/vibesync/common/v1/common_pb';
import { RoomPermission, type Member } from '../gen/vibesync/room/v1/room_pb';

export interface MemberListProps {
  members: Member[];
  /** Whether the local user is the room owner (shows the grant editor). */
  isOwner?: boolean;
  /** Owner action: replace a member's permission set. */
  onGrant?: (userId: string, permissions: RoomPermission[]) => void;
  /** True while a grant mutation is in flight for this member. */
  grantingUserId?: string | null;
}

const ROLE_BADGE: Record<RoomRole, string> = {
  [RoomRole.UNSPECIFIED]: 'bg-gray-600/30 text-gray-400',
  [RoomRole.GUEST]: 'bg-gray-500/20 text-gray-400',
  [RoomRole.MEMBER]: 'bg-gray-500/20 text-gray-300',
  [RoomRole.MODERATOR]: 'bg-blue-500/15 text-blue-300',
  [RoomRole.OWNER]: 'bg-purple-500/20 text-purple-300',
};

const ROLE_LABEL: Record<RoomRole, string> = {
  [RoomRole.UNSPECIFIED]: 'unknown',
  [RoomRole.GUEST]: 'guest',
  [RoomRole.MEMBER]: 'member',
  [RoomRole.MODERATOR]: 'moderator',
  [RoomRole.OWNER]: 'owner',
};

/** Grantable controls in display order (ADR-0017). */
const GRANTABLE: Array<{ perm: RoomPermission; label: string; hint: string }> = [
  { perm: RoomPermission.SEEK, label: 'Seek', hint: 'Rewind the room clock for everyone' },
  { perm: RoomPermission.PAUSE_PLAY, label: 'Play/Pause', hint: 'Pause and resume playback' },
  { perm: RoomPermission.SWITCH_QUEUE, label: 'Switch', hint: 'Load another queue item' },
  { perm: RoomPermission.ADD_QUEUE, label: 'Add', hint: 'Add media to the queue' },
  { perm: RoomPermission.REMOVE_QUEUE, label: 'Remove', hint: 'Remove media from the queue' },
];

export default function MemberList({
  members,
  isOwner = false,
  onGrant,
  grantingUserId = null,
}: MemberListProps) {
  if (members.length === 0) {
    return <p className="p-4 text-sm text-gray-400">No members yet.</p>;
  }

  return (
    <ul className="divide-y divide-gray-800">
      {members.map((member) => {
        const idValue = member.userId?.value ?? 'unknown';
        const initials = idValue.slice(0, 2).toUpperCase();
        const joined = member.joinedAt?.toDate();
        const isOwnerRow = member.role === RoomRole.OWNER;
        const granted = new Set(member.permissions);
        const granting = grantingUserId === idValue;
        return (
          <li key={idValue} className="px-4 py-3">
            <div className="flex items-center gap-3">
              <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-surface-overlay text-xs font-semibold text-gray-300">
                {initials}
              </div>
              <div className="min-w-0 flex-1">
                <p className="truncate font-mono text-sm text-gray-200">{idValue}</p>
                <p className="text-xs text-gray-500">
                  {joined ? `joined ${joined.toLocaleDateString()}` : 'join date unknown'}
                </p>
              </div>
              <span
                className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${
                  ROLE_BADGE[member.role] ?? ROLE_BADGE[RoomRole.UNSPECIFIED]
                }`}
              >
                {ROLE_LABEL[member.role] ?? ROLE_LABEL[RoomRole.UNSPECIFIED]}
              </span>
            </div>

            {isOwner && !isOwnerRow && onGrant && (
              <div className="mt-2 flex flex-wrap gap-1.5">
                {GRANTABLE.map(({ perm, label, hint }) => {
                  const active = granted.has(perm);
                  return (
                    <button
                      key={perm}
                      type="button"
                      disabled={granting}
                      title={hint}
                      aria-pressed={active}
                      className={`rounded-full px-2.5 py-1 text-xs font-medium transition-colors disabled:opacity-50 ${
                        active
                          ? 'bg-accent/25 text-accent hover:bg-accent/35'
                          : 'bg-surface-overlay text-gray-400 hover:text-gray-200'
                      }`}
                      onClick={() => {
                        const next = GRANTABLE.map((g) => g.perm).filter(
                          (p) => (p === perm ? !active : granted.has(p)),
                        );
                        onGrant(idValue, next);
                      }}
                    >
                      {label}
                    </button>
                  );
                })}
              </div>
            )}
            {isOwnerRow && (
              <p className="mt-1 text-xs text-gray-600">Holds every permission (room owner).</p>
            )}
          </li>
        );
      })}
    </ul>
  );
}
