import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { getMediaClient, getRoomClient, getSyncClient } from '../api/clients';
import AddVideoPanel from '../components/AddVideoPanel';
import MemberList from '../components/MemberList';
import PlayerControls, { type CommandOptions } from '../components/PlayerControls';
import QueuePanel from '../components/QueuePanel';
import SearchPanel from '../components/SearchPanel';
import SyncIndicator from '../components/SyncIndicator';
import UploadPanel from '../components/UploadPanel';
import YouTubePlayer from '../components/YouTubePlayer';
import { useHeartbeat } from '../hooks/useHeartbeat';
import { useSyncStream } from '../hooks/useSyncStream';
import { useAuthStore } from '../stores/auth';
import { Id } from '../gen/vibesync/common/v1/common_pb';
import { MediaKind, MediaSource, type Media } from '../gen/vibesync/media/v1/media_pb';
import { RoomPermission, type RoomPermission as RoomPermissionType } from '../gen/vibesync/room/v1/room_pb';
import { CommandKind, CommandRequest, type SyncState } from '../gen/vibesync/sync/v1/sync_pb';

type SidebarTab = 'queue' | 'members' | 'add' | 'upload';

function mediaDurationMs(media: Media | undefined): number {
  if (!media?.duration) return 0;
  return Number(media.duration.seconds) * 1000 + media.duration.nanos / 1_000_000;
}

export default function RoomPage() {
  const { roomId } = useParams<{ roomId: string }>();
  const activeRoomId = roomId ?? '';

  // Live sync state + connection health.
  const { syncState, connectionStatus, advanceMediaTime: advanceFromState } = useSyncStream(activeRoomId);

  // Mirror sync state into a ref so the heartbeat getter and command sender
  // always read the latest values without re-creating their callbacks.
  const syncStateRef = useRef<SyncState | null>(null);
  useEffect(() => {
    syncStateRef.current = syncState;
  }, [syncState]);

  const getMediaTimeMs = useCallback(() => advanceFromState(syncStateRef.current), [advanceFromState]);
  const { smoothedRttMs, clientDriftMs } = useHeartbeat({ roomId: activeRoomId, getMediaTimeMs });

  // Room details, members, queue.
  const roomQuery = useQuery({
    queryKey: ['room', activeRoomId],
    enabled: activeRoomId !== '',
    // GetRoomRequest.lookup is a oneof: it must be set via the group form
    // ({ lookup: { case: 'id', value } }) — the bare { id } field form is
    // silently ignored by protobuf-es initPartial.
    queryFn: () =>
      getRoomClient().getRoom({
        lookup: { case: 'id', value: { value: activeRoomId } },
      }),
  });

  const membersQuery = useQuery({
    queryKey: ['members', activeRoomId],
    enabled: activeRoomId !== '',
    refetchInterval: 30_000,
    queryFn: () => getRoomClient().getMembers({ roomId: { value: activeRoomId } }),
  });

  const queueQuery = useQuery({
    queryKey: ['queue', activeRoomId],
    enabled: activeRoomId !== '',
    refetchInterval: 10_000,
    queryFn: () => getMediaClient().getQueue({ roomId: { value: activeRoomId } }),
  });

  const queueItems = useMemo(() => queueQuery.data?.items ?? [], [queueQuery.data]);

  // Resolve details for every queued media (titles, covers, durations).
  const mediaQueries = useQueries({
    queries: queueItems
      .filter((item) => item.mediaId != null)
      .map((item) => ({
        queryKey: ['media', item.mediaId?.value ?? ''],
        queryFn: () => getMediaClient().getMedia({ id: { value: item.mediaId?.value ?? '' } }),
        staleTime: 60_000,
      })),
  });

  const mediaDetails = useMemo(() => {
    const map: Record<string, Media> = {};
    for (const result of mediaQueries) {
      const media = result.data?.media;
      if (media?.id) map[media.id.value] = media;
    }
    return map;
  }, [mediaQueries]);

  // The media the authoritative state says is loaded (may not be in the queue).
  const currentMediaId = syncState?.mediaId?.value ?? null;
  const currentMediaQuery = useQuery({
    queryKey: ['media', currentMediaId],
    enabled: currentMediaId != null,
    staleTime: 60_000,
    queryFn: () => getMediaClient().getMedia({ id: { value: currentMediaId ?? '' } }),
  });
  const currentMedia =
    (currentMediaId != null ? mediaDetails[currentMediaId] : undefined) ??
    currentMediaQuery.data?.media;

  // Identity + control grants (ADR-0017). The host and the room owner hold
  // every control implicitly; a guest holds exactly the permissions the
  // owner granted them (from the members list).
  const userId = useAuthStore((s) => s.userId);
  const isHost = syncState?.hostId != null && syncState.hostId.value === userId;
  const room = roomQuery.data?.room;
  const isRoomOwner = room?.ownerId != null && room.ownerId.value === userId;
  const myMember = membersQuery.data?.members.find((m) => m.userId?.value === userId);
  const myPermissions = new Set(myMember?.permissions ?? []);
  const controlsAll = isHost || isRoomOwner;
  const canSeek = controlsAll || myPermissions.has(RoomPermission.SEEK);
  const canPlayPause = controlsAll || myPermissions.has(RoomPermission.PAUSE_PLAY);
  const canSwitchQueue = controlsAll || myPermissions.has(RoomPermission.SWITCH_QUEUE);
  const canAddQueue = isRoomOwner || myPermissions.has(RoomPermission.ADD_QUEUE);
  const canRemoveQueue = isRoomOwner || myPermissions.has(RoomPermission.REMOVE_QUEUE);

  // Add-by-link YouTube items carry no duration in metadata (oEmbed has
  // none); the IFrame player reports the real one once it loads.
  const [playerDurationMs, setPlayerDurationMs] = useState<number | null>(null);
  useEffect(() => {
    setPlayerDurationMs(null);
  }, [currentMediaId]);
  const effectiveDurationMs = currentMedia
    ? mediaDurationMs(currentMedia) || playerDurationMs || null
    : null;

  const isYouTubeVideo =
    currentMedia?.kind === MediaKind.VIDEO &&
    currentMedia?.source === MediaSource.PROVIDER &&
    currentMedia.externalRef.length > 0;

  const [tab, setTab] = useState<SidebarTab>('queue');
  const queryClient = useQueryClient();

  /** Owner action: replace a member's permission set (ADR-0017). */
  const grantPermissions = useMutation({
    mutationFn: (input: { userId: string; permissions: RoomPermissionType[] }) =>
      getRoomClient().grantPermissions({
        roomId: { value: activeRoomId },
        userId: { value: input.userId },
        permissions: input.permissions,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['members', activeRoomId] });
    },
  });

  /** Sends a playback command stamped with the latest fencing token. */
  const sendCommand = useCallback(
    (kind: CommandKind, opts?: CommandOptions) => {
      if (!activeRoomId) return;
      const request = new CommandRequest({
        roomId: { value: activeRoomId },
        kind,
        fencingToken: syncStateRef.current?.fencingToken ?? 0n,
      });
      if (opts?.seekToMs !== undefined) {
        request.seekToMs = BigInt(Math.max(0, Math.round(opts.seekToMs)));
      }
      if (opts?.rate !== undefined) {
        request.rate = opts.rate;
      }
      if (opts?.mediaId !== undefined) {
        request.mediaId = new Id({ value: opts.mediaId });
      }
      void getSyncClient().command(request).catch((err: unknown) => {
        console.error('playback command failed', err);
      });
    },
    [activeRoomId],
  );

  /** Loads a queued media into the room, resuming playback when asked. */
  const loadMedia = useCallback(
    (mediaId: string, keepPlaying: boolean) => {
      // LOAD_MEDIA resets to position 0 paused; follow with PLAY when the
      // room was playing so switching never stops the session.
      sendCommand(CommandKind.LOAD_MEDIA, { mediaId });
      if (keepPlaying) {
        sendCommand(CommandKind.PLAY);
      }
    },
    [sendCommand],
  );

  /** Loads a queued media into the room and starts playback. */
  const playNow = useCallback((mediaId: string) => loadMedia(mediaId, true), [loadMedia]);

  /**
   * NEXT/PREVIOUS are resolved client-side from the queue (the sync
   * protocol accepts them as no-ops — the queue lives in the Media
   * Service): NEXT loads the item after the current one, PREVIOUS loads
   * the one before it, or restarts the current item at the queue head.
   */
  const skip = useCallback(
    (dir: 1 | -1) => {
      const items = queueItems.filter((i) => i.mediaId?.value);
      if (items.length === 0) return;
      const idx = items.findIndex((i) => i.mediaId?.value === currentMediaId);
      if (dir > 0) {
        const next = (idx >= 0 ? items[idx + 1] : items[0])?.mediaId?.value;
        if (next) loadMedia(next, true);
        return;
      }
      const prev = idx > 0 ? items[idx - 1]?.mediaId?.value : undefined;
      if (prev) {
        loadMedia(prev, true);
        return;
      }
      // Queue head (or current media not queued): restart from the top.
      sendCommand(CommandKind.SEEK, { seekToMs: 0 });
    },
    [queueItems, currentMediaId, loadMedia, sendCommand],
  );

  /** Transport dispatch: resolve NEXT/PREVIOUS locally, pass the rest. */
  const dispatchCommand = useCallback(
    (kind: CommandKind, opts?: CommandOptions) => {
      if (kind === CommandKind.NEXT) {
        skip(1);
        return;
      }
      if (kind === CommandKind.PREVIOUS) {
        skip(-1);
        return;
      }
      sendCommand(kind, opts);
    },
    [skip, sendCommand],
  );

  if (!roomId) {
    return (
      <div className="p-8 text-sm text-gray-400">
        Missing room id. <Link to="/rooms" className="text-accent hover:underline">Back to rooms</Link>
      </div>
    );
  }

  const memberCount = membersQuery.data?.members.length ?? room?.memberCount ?? 0;

  const tabs: Array<{ id: SidebarTab; label: string }> = [
    { id: 'queue', label: `Queue (${queueItems.length})` },
    { id: 'members', label: `Members (${memberCount})` },
    { id: 'add', label: 'Add media' },
    { id: 'upload', label: 'Upload' },
  ];

  return (
    <div className="flex min-h-screen flex-col">
      <header className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-gray-800 bg-surface-raised px-6 py-3">
        <div className="min-w-0">
          <Link to="/rooms" className="text-xs text-gray-400 transition-colors hover:text-gray-200">
            ← Rooms
          </Link>
          <h1 className="truncate text-lg font-semibold">
            {room?.name ?? 'Room'}
            {room?.slug ? (
              <span className="ml-2 align-middle font-mono text-xs font-normal text-gray-500">
                {room.slug}
              </span>
            ) : null}
          </h1>
        </div>

        {/* Room transport: prev / play-pause / seek / next (ADR-0017 grants). */}
        <div className="order-3 flex min-w-0 flex-1 justify-center lg:order-none">
          <PlayerControls
            syncState={syncState}
            onCommand={dispatchCommand}
            mediaDurationMs={effectiveDurationMs}
            canSeek={canSeek}
            canPlayPause={canPlayPause}
            canSwitch={canSwitchQueue}
          />
        </div>

        <SyncIndicator
          driftMs={clientDriftMs}
          rttMs={smoothedRttMs}
          connectionStatus={connectionStatus}
        />
      </header>

      <div className="flex flex-1 flex-col lg:flex-row">
        <main className="flex-1 space-y-6 p-6">
          {isYouTubeVideo && currentMedia ? (
            <YouTubePlayer
              key={currentMedia.externalRef}
              videoId={currentMedia.externalRef}
              syncState={syncState}
              onDuration={setPlayerDurationMs}
            />
          ) : (
            <section className="card flex items-center justify-center py-12 text-sm text-gray-400">
              Nothing loaded. Add a YouTube link or a Spotify track to start the room clock.
            </section>
          )}
        </main>

        <aside className="w-full shrink-0 border-t border-gray-800 bg-surface-raised/40 lg:w-96 lg:border-l lg:border-t-0">
          <div className="flex border-b border-gray-800">
            {tabs.map((t) => (
              <button
                key={t.id}
                type="button"
                className={`flex-1 px-3 py-2.5 text-xs font-medium transition-colors ${
                  tab === t.id
                    ? 'border-b-2 border-accent text-gray-100'
                    : 'text-gray-400 hover:text-gray-200'
                }`}
                onClick={() => setTab(t.id)}
              >
                {t.label}
              </button>
            ))}
          </div>

          <div className="max-h-[calc(100vh-8rem)] overflow-y-auto">
            {tab === 'queue' && (
              <QueuePanel
                roomId={roomId}
                queueItems={queueItems}
                mediaDetails={mediaDetails}
                currentMediaId={currentMediaId}
                canSwitch={canSwitchQueue}
                canRemove={canRemoveQueue}
                onPlayNow={canSwitchQueue ? playNow : undefined}
              />
            )}
            {tab === 'members' && (
              <MemberList
                members={membersQuery.data?.members ?? []}
                isOwner={isRoomOwner}
                grantingUserId={grantPermissions.isPending ? grantPermissions.variables?.userId ?? null : null}
                onGrant={
                  isRoomOwner
                    ? (userId, permissions) => grantPermissions.mutate({ userId, permissions })
                    : undefined
                }
              />
            )}
            {tab === 'add' && (
              <div>
                <AddVideoPanel roomId={roomId} canAdd={canAddQueue} />
                <SearchPanel roomId={roomId} canAdd={canAddQueue} />
              </div>
            )}
            {tab === 'upload' && <UploadPanel />}
          </div>
        </aside>
      </div>
    </div>
  );
}
