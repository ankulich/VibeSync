import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useQueries, useQuery } from '@tanstack/react-query';
import { getMediaClient, getRoomClient, getSyncClient } from '../api/clients';
import MemberList from '../components/MemberList';
import PlayerControls, { type CommandOptions } from '../components/PlayerControls';
import QueuePanel from '../components/QueuePanel';
import SearchPanel from '../components/SearchPanel';
import SyncIndicator from '../components/SyncIndicator';
import { useHeartbeat } from '../hooks/useHeartbeat';
import { useSyncStream } from '../hooks/useSyncStream';
import { MediaKind, MediaSource, type Media } from '../gen/vibesync/media/v1/media_pb';
import { CommandKind, CommandRequest, type SyncState } from '../gen/vibesync/sync/v1/sync_pb';

type SidebarTab = 'queue' | 'members' | 'search';

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

  const [tab, setTab] = useState<SidebarTab>('queue');

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
      void getSyncClient().command(request).catch((err: unknown) => {
        console.error('playback command failed', err);
      });
    },
    [activeRoomId],
  );

  if (!roomId) {
    return (
      <div className="p-8 text-sm text-gray-400">
        Missing room id. <Link to="/rooms" className="text-accent hover:underline">Back to rooms</Link>
      </div>
    );
  }

  const room = roomQuery.data?.room;
  const memberCount = membersQuery.data?.members.length ?? room?.memberCount ?? 0;

  const tabs: Array<{ id: SidebarTab; label: string }> = [
    { id: 'queue', label: `Queue (${queueItems.length})` },
    { id: 'members', label: `Members (${memberCount})` },
    { id: 'search', label: 'Search' },
  ];

  return (
    <div className="flex min-h-screen flex-col">
      <header className="flex flex-wrap items-center justify-between gap-3 border-b border-gray-800 bg-surface-raised px-6 py-3">
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
        <SyncIndicator
          driftMs={clientDriftMs}
          rttMs={smoothedRttMs}
          connectionStatus={connectionStatus}
        />
      </header>

      <div className="flex flex-1 flex-col lg:flex-row">
        <main className="flex-1 space-y-6 p-6">
          <PlayerControls
            syncState={syncState}
            onCommand={sendCommand}
            mediaTitle={currentMedia?.title ?? null}
            mediaDurationMs={currentMedia ? mediaDurationMs(currentMedia) : null}
          />

          <section className="card">
            <h2 className="text-xs font-semibold uppercase tracking-wide text-gray-400">
              Now playing
            </h2>
            {currentMedia ? (
              <div className="mt-3 flex items-center gap-4">
                {currentMedia.coverUrl ? (
                  <img
                    src={currentMedia.coverUrl}
                    alt=""
                    className="h-14 w-14 shrink-0 rounded-lg object-cover"
                  />
                ) : (
                  <div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-lg bg-surface-overlay text-gray-500">
                    ♪
                  </div>
                )}
                <div className="min-w-0">
                  <p className="truncate font-medium">{currentMedia.title}</p>
                  <p className="truncate text-sm text-gray-400">
                    {currentMedia.artist || 'Unknown artist'}
                  </p>
                  <p className="mt-1 truncate text-xs text-gray-500">
                    {MediaKind[currentMedia.kind] ?? 'UNKNOWN'} · via{' '}
                    {MediaSource[currentMedia.source] ?? 'UNKNOWN'} · ref {currentMedia.externalRef}
                  </p>
                </div>
              </div>
            ) : (
              <p className="mt-3 text-sm text-gray-400">
                Nothing loaded. Queue a track from Search to start the room clock.
              </p>
            )}
          </section>
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
              />
            )}
            {tab === 'members' && <MemberList members={membersQuery.data?.members ?? []} />}
            {tab === 'search' && <SearchPanel roomId={roomId} />}
          </div>
        </aside>
      </div>
    </div>
  );
}
