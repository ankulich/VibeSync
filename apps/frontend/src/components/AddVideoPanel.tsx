import { useState, type FormEvent } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { getMediaClient, getProviderClient } from '../api/clients';
import { MediaKind, MediaSource } from '../gen/vibesync/media/v1/media_pb';
import { ProviderName } from '../gen/vibesync/provider/v1/provider_pb';

export interface AddVideoPanelProps {
  roomId: string;
}

const VIDEO_ID_RE = /^[\w-]{11}$/;

/**
 * Extracts a YouTube video id from a raw id or any common URL shape
 * (watch?v=, youtu.be/<id>, /shorts/<id>, /embed/<id>, /live/<id>).
 * Returns null when the input is neither.
 */
export function parseYouTubeId(raw: string): string | null {
  const input = raw.trim();
  if (VIDEO_ID_RE.test(input)) return input;
  let url: URL;
  try {
    url = new URL(input);
  } catch {
    return null;
  }
  const host = url.hostname.replace(/^www\./, '').toLowerCase();
  if (host === 'youtu.be') {
    const id = url.pathname.split('/').filter(Boolean)[0] ?? '';
    return VIDEO_ID_RE.test(id) ? id : null;
  }
  if (host === 'youtube.com' || host === 'm.youtube.com' || host === 'music.youtube.com') {
    const fromQuery = url.searchParams.get('v');
    if (fromQuery && VIDEO_ID_RE.test(fromQuery)) return fromQuery;
    const match = url.pathname.match(/\/(?:shorts|embed|live|v)\/([\w-]{11})/);
    if (match) return match[1];
  }
  return null;
}

/**
 * Adds a YouTube video to the room queue by URL. There is no server-side
 * YouTube search without the Data API (see ADR-0016), so pasting a link is
 * the way in; metadata (title/channel/cover) is resolved keyless via oEmbed
 * by the Provider Service.
 */
export default function AddVideoPanel({ roomId }: AddVideoPanelProps) {
  const [input, setInput] = useState('');
  const queryClient = useQueryClient();

  const addVideo = useMutation({
    mutationFn: async (raw: string) => {
      const videoId = parseYouTubeId(raw);
      if (!videoId) {
        throw new Error('That does not look like a YouTube link or video id.');
      }
      // Resolve metadata (title, channel, cover) via oEmbed; the video id is
      // the playable reference for the IFrame player.
      const resolved = await getProviderClient().resolve({
        provider: ProviderName.YOUTUBE,
        externalRef: videoId,
      });
      const { media } = await getMediaClient().createMedia({
        kind: MediaKind.VIDEO,
        source: MediaSource.PROVIDER,
        externalRef: videoId,
        title: resolved.title || 'YouTube video',
        artist: resolved.artist,
        coverUrl: resolved.coverUrl,
      });
      if (!media?.id) {
        throw new Error('Media creation returned no id');
      }
      await getMediaClient().addToQueue({ roomId: { value: roomId }, mediaId: media.id });
      return videoId;
    },
    onSuccess: () => {
      setInput('');
      void queryClient.invalidateQueries({ queryKey: ['queue', roomId] });
      // Refetch fresh metadata for the newly created media entry.
      void queryClient.invalidateQueries({ queryKey: ['media'] });
    },
  });

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const raw = input.trim();
    if (raw.length === 0) return;
    addVideo.mutate(raw);
  };

  const onInputChange = (value: string) => {
    setInput(value);
    if (addVideo.isSuccess || addVideo.isError) addVideo.reset();
  };

  return (
    <div className="flex flex-col gap-2 border-b border-gray-800 p-4">
      <p className="text-xs font-semibold uppercase tracking-wide text-gray-400">YouTube video</p>
      <form onSubmit={onSubmit} className="flex gap-2">
        <input
          className="input"
          placeholder="Paste a YouTube link…"
          value={input}
          onChange={(e) => onInputChange(e.target.value)}
          aria-label="YouTube link"
        />
        <button
          type="submit"
          className="btn btn-primary shrink-0"
          disabled={input.trim().length === 0 || addVideo.isPending}
        >
          {addVideo.isPending ? '…' : 'Add'}
        </button>
      </form>
      {addVideo.isSuccess && (
        <p className="text-xs text-emerald-400">Added to queue ✓</p>
      )}
      {addVideo.isError && (
        <p className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-300">
          {(addVideo.error as Error | null)?.message ?? 'Could not add the video.'}
        </p>
      )}
      <p className="text-xs text-gray-500">
        Everyone watches in sync: the host controls playback; quality and subtitles stay personal.
      </p>
    </div>
  );
}
