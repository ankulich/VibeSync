import { useState, type FormEvent } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import { getRoomClient } from '../api/clients';
import { RoomVisibility, type Room } from '../gen/vibesync/room/v1/room_pb';
import { useAuthStore } from '../stores/auth';

const VISIBILITY_BADGE: Record<RoomVisibility, string> = {
  [RoomVisibility.UNSPECIFIED]: 'bg-gray-500/20 text-gray-300',
  [RoomVisibility.PUBLIC]: 'bg-emerald-500/15 text-emerald-300',
  [RoomVisibility.UNLISTED]: 'bg-yellow-500/15 text-yellow-300',
  [RoomVisibility.PRIVATE]: 'bg-red-500/15 text-red-300',
};

const VISIBILITY_LABEL: Record<RoomVisibility, string> = {
  [RoomVisibility.UNSPECIFIED]: 'Unknown',
  [RoomVisibility.PUBLIC]: 'Public',
  [RoomVisibility.UNLISTED]: 'Unlisted',
  [RoomVisibility.PRIVATE]: 'Private',
};

function RoomCard({
  room,
  joining,
  onJoin,
}: {
  room: Room;
  joining: boolean;
  onJoin: (roomId: string) => void;
}) {
  const id = room.id?.value ?? '';
  return (
    <div className="card flex flex-col gap-3">
      <div className="flex items-start justify-between gap-3">
        <h3 className="truncate text-base font-semibold">{room.name || room.slug || 'Untitled room'}</h3>
        <span
          className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${
            VISIBILITY_BADGE[room.visibility] ?? VISIBILITY_BADGE[RoomVisibility.UNSPECIFIED]
          }`}
        >
          {VISIBILITY_LABEL[room.visibility] ?? 'Unknown'}
        </span>
      </div>
      <p className="line-clamp-2 min-h-[2rem] text-sm text-gray-400">
        {room.description || 'No description.'}
      </p>
      <div className="mt-auto flex items-center justify-between">
        <span className="text-xs text-gray-500">
          {room.memberCount}
          {room.maxMembers > 0 ? ` / ${room.maxMembers}` : ''} members
        </span>
        <button
          type="button"
          className="btn btn-primary px-4 py-1.5 text-xs"
          disabled={!id || joining}
          onClick={() => onJoin(id)}
        >
          {joining ? 'Joining…' : 'Join'}
        </button>
      </div>
    </div>
  );
}

export default function RoomsPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const logout = useAuthStore((s) => s.logout);
  const userId = useAuthStore((s) => s.userId);

  const [showCreate, setShowCreate] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [visibility, setVisibility] = useState<RoomVisibility>(RoomVisibility.PUBLIC);
  const [joinError, setJoinError] = useState<string | null>(null);

  const roomsQuery = useQuery({
    queryKey: ['rooms'],
    queryFn: () => getRoomClient().listRooms({ page: { limit: 20 } }),
  });

  const createMutation = useMutation({
    mutationFn: (input: { name: string; description: string; visibility: RoomVisibility }) =>
      getRoomClient().createRoom(input),
    onSuccess: (data) => {
      void queryClient.invalidateQueries({ queryKey: ['rooms'] });
      const id = data.room?.id?.value;
      if (id) navigate(`/room/${id}`);
      else setShowCreate(false);
    },
  });

  const joinMutation = useMutation({
    mutationFn: async (roomId: string) => {
      await getRoomClient().joinRoom({ roomId: { value: roomId } });
      return roomId;
    },
    onSuccess: (roomId) => {
      setJoinError(null);
      navigate(`/room/${roomId}`);
    },
    onError: (err) => {
      setJoinError(err instanceof Error ? err.message : 'Could not join room.');
    },
  });

  const onCreateSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) return;
    createMutation.mutate({
      name: trimmed,
      description: description.trim(),
      visibility,
    });
  };

  const rooms = roomsQuery.data?.rooms ?? [];

  return (
    <div className="mx-auto min-h-screen w-full max-w-5xl p-6">
      <header className="mb-8 flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">
            Vibe<span className="text-accent">Sync</span> Rooms
          </h1>
          <p className="mt-1 font-mono text-xs text-gray-500">{userId ?? ''}</p>
        </div>
        <div className="flex items-center gap-2">
          <Link to="/profile" className="btn btn-ghost">
            Profile
          </Link>
          <button
            type="button"
            className="btn btn-ghost"
            onClick={() => {
              void logout().finally(() => navigate('/login'));
            }}
          >
            Sign out
          </button>
        </div>
      </header>

      <div className="mb-6">
        {showCreate ? (
          <form onSubmit={onCreateSubmit} className="card space-y-3">
            <div>
              <label htmlFor="room-name" className="mb-1 block text-xs font-medium text-gray-400">
                Name
              </label>
              <input
                id="room-name"
                className="input"
                placeholder="Late night coding session"
                value={name}
                onChange={(e) => setName(e.target.value)}
                maxLength={80}
                required
              />
            </div>
            <div>
              <label htmlFor="room-description" className="mb-1 block text-xs font-medium text-gray-400">
                Description
              </label>
              <input
                id="room-description"
                className="input"
                placeholder="Lo-fi and deep focus (optional)"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                maxLength={280}
              />
            </div>
            <div className="flex items-center gap-3">
              <label htmlFor="room-visibility" className="text-xs font-medium text-gray-400">
                Visibility
              </label>
              <select
                id="room-visibility"
                className="input max-w-[12rem]"
                value={visibility}
                onChange={(e) => setVisibility(Number(e.target.value) as RoomVisibility)}
              >
                <option value={RoomVisibility.PUBLIC}>Public</option>
                <option value={RoomVisibility.UNLISTED}>Unlisted</option>
                <option value={RoomVisibility.PRIVATE}>Private</option>
              </select>
            </div>
            {createMutation.isError && (
              <p className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-300">
                {(createMutation.error as Error | null)?.message ?? 'Could not create room.'}
              </p>
            )}
            <div className="flex gap-2">
              <button
                type="submit"
                className="btn btn-primary"
                disabled={createMutation.isPending || name.trim().length === 0}
              >
                {createMutation.isPending ? 'Creating…' : 'Create room'}
              </button>
              <button type="button" className="btn btn-ghost" onClick={() => setShowCreate(false)}>
                Cancel
              </button>
            </div>
          </form>
        ) : (
          <button type="button" className="btn btn-primary" onClick={() => setShowCreate(true)}>
            + New room
          </button>
        )}
      </div>

      {roomsQuery.isLoading && <p className="text-sm text-gray-400">Loading rooms…</p>}

      {roomsQuery.isError && (
        <p className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-300">
          Could not load rooms. {(roomsQuery.error as Error | null)?.message ?? ''}
        </p>
      )}

      {joinError && (
        <p className="mb-4 rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-300">
          {joinError}
        </p>
      )}

      {!roomsQuery.isLoading && !roomsQuery.isError && rooms.length === 0 && (
        <p className="text-sm text-gray-400">
          No rooms yet — create the first one and invite your friends.
        </p>
      )}

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {rooms.map((room) => (
          <RoomCard
            key={room.id?.value ?? room.slug}
            room={room}
            joining={joinMutation.isPending && joinMutation.variables === (room.id?.value ?? '')}
            onJoin={(id) => joinMutation.mutate(id)}
          />
        ))}
      </div>
    </div>
  );
}
