import { RoomRole } from '../gen/vibesync/common/v1/common_pb';
import type { Member } from '../gen/vibesync/room/v1/room_pb';

export interface MemberListProps {
  members: Member[];
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

export default function MemberList({ members }: MemberListProps) {
  if (members.length === 0) {
    return <p className="p-4 text-sm text-gray-400">No members yet.</p>;
  }

  return (
    <ul className="divide-y divide-gray-800">
      {members.map((member) => {
        const idValue = member.userId?.value ?? 'unknown';
        const initials = idValue.slice(0, 2).toUpperCase();
        const joined = member.joinedAt?.toDate();
        return (
          <li key={idValue} className="flex items-center gap-3 px-4 py-3">
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
          </li>
        );
      })}
    </ul>
  );
}
