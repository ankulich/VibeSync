import { useMutation, useQueryClient } from '@tanstack/react-query';
import { getMediaClient } from '../api/clients';
import type { Media, QueueItem } from '../gen/vibesync/media/v1/media_pb';
import { formatTime } from '../lib/sync';

export interface QueuePanelProps {
  roomId: string;
  queueItems: QueueItem[];
  /** Media details keyed by media id, resolved by the parent page. */
  mediaDetails: Record<string, Media>;
  /** Media id currently loaded in the player, if any. */
  currentMediaId?: string | null;
  /** Whether the local user may switch queue items (host/owner/grant). */
  canSwitch?: boolean;
  /** Whether the local user may remove from the queue (owner/grant). */
  canRemove?: boolean;
  /** Loads a queued media into the room; undefined when not permitted. */
  onPlayNow?: (mediaId: string) => void;
}

function mediaDurationMs(media: Media | undefined): number {
  if (!media?.duration) return 0;
  return Number(media.duration.seconds) * 1000 + media.duration.nanos / 1_000_000;
}

export default function QueuePanel({
  roomId,
  queueItems,
  mediaDetails,
  currentMediaId,
  canSwitch = true,
  canRemove = true,
  onPlayNow,
}: QueuePanelProps) {
  const queryClient = useQueryClient();

  const removeMutation = useMutation({
    mutationFn: (position: number) =>
      getMediaClient().removeFromQueue({ roomId: { value: roomId }, position }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['queue', roomId] });
    },
  });

  if (queueItems.length === 0) {
    return <p className="p-4 text-sm text-gray-400">Queue is empty — add something from Search.</p>;
  }

  return (
    <ul className="divide-y divide-gray-800">
      {queueItems.map((item) => {
        const mediaId = item.mediaId?.value ?? '';
        const media = mediaDetails[mediaId];
        const isCurrent = currentMediaId != null && mediaId === currentMediaId;
        const removing = removeMutation.isPending && removeMutation.variables === item.position;
        return (
          <li
            key={`${item.position}-${mediaId}`}
            className={`flex items-center gap-3 px-4 py-3 ${
              isCurrent ? 'border-l-2 border-accent bg-accent/10' : ''
            }`}
          >
            <span className="w-5 text-center text-xs text-gray-500">{item.position}</span>
            {media?.coverUrl ? (
              <img src={media.coverUrl} alt="" className="h-10 w-10 shrink-0 rounded object-cover" />
            ) : (
              <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded bg-surface-overlay text-xs text-gray-500">
                ♪
              </div>
            )}
            <div className="min-w-0 flex-1">
              <p className={`truncate text-sm ${isCurrent ? 'font-semibold text-gray-100' : 'text-gray-200'}`}>
                {media?.title ?? (mediaId ? 'Loading…' : 'Unknown media')}
              </p>
              <p className="truncate text-xs text-gray-500">
                {media?.artist || 'Unknown artist'} · {formatTime(mediaDurationMs(media))}
              </p>
            </div>
            {isCurrent && (
              <span className="rounded-full bg-accent/20 px-2 py-0.5 text-xs text-accent">now</span>
            )}
            {onPlayNow && !isCurrent && (
              <button
                type="button"
                className="rounded px-2 py-1 text-xs text-gray-500 transition-colors hover:bg-surface-overlay hover:text-accent disabled:cursor-not-allowed disabled:opacity-50"
                disabled={!canSwitch || mediaId === ''}
                onClick={() => onPlayNow(mediaId)}
                aria-label="Play now"
                title={canSwitch ? 'Play now' : 'Switching requires the owner to grant permission'}
              >
                ▶ Play
              </button>
            )}
            <button
              type="button"
              className="rounded px-2 py-1 text-xs text-gray-500 transition-colors hover:bg-surface-overlay hover:text-red-300 disabled:cursor-not-allowed disabled:opacity-50"
              disabled={removing || !canRemove}
              onClick={() => removeMutation.mutate(item.position)}
              aria-label="Remove from queue"
              title={canRemove ? 'Remove from queue' : 'Removing requires the owner to grant permission'}
            >
              {removing ? '…' : 'Remove'}
            </button>
          </li>
        );
      })}
    </ul>
  );
}
