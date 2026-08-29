import { useState, type FormEvent } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getMediaClient, getProviderClient } from '../api/clients';
import { MediaKind, MediaSource } from '../gen/vibesync/media/v1/media_pb';
import { ProviderName, type SearchResult } from '../gen/vibesync/provider/v1/provider_pb';
import { formatTime } from '../lib/sync';

export interface SearchPanelProps {
  roomId: string;
}

export default function SearchPanel({ roomId }: SearchPanelProps) {
  const [provider, setProvider] = useState<ProviderName>(ProviderName.SPOTIFY);
  const [input, setInput] = useState('');
  const [submittedQuery, setSubmittedQuery] = useState('');
  const [addedRefs, setAddedRefs] = useState<Set<string>>(new Set());
  const queryClient = useQueryClient();

  const searchQuery = useQuery({
    queryKey: ['provider-search', provider, submittedQuery],
    enabled: submittedQuery.length > 0,
    queryFn: () =>
      getProviderClient().search({
        provider,
        query: submittedQuery,
        page: { limit: 20 },
      }),
  });

  const addToQueue = useMutation({
    mutationFn: async (result: SearchResult) => {
      const { media } = await getMediaClient().createMedia({
        kind: provider === ProviderName.YOUTUBE ? MediaKind.VIDEO : MediaKind.AUDIO,
        source: MediaSource.PROVIDER,
        externalRef: result.externalRef,
        title: result.title,
        artist: result.artist,
        durationMs: result.durationMs,
        coverUrl: result.coverUrl,
      });
      if (!media?.id) {
        throw new Error('Media creation returned no id');
      }
      await getMediaClient().addToQueue({ roomId: { value: roomId }, mediaId: media.id });
    },
    onSuccess: (_data, result) => {
      setAddedRefs((prev) => new Set(prev).add(result.externalRef));
      void queryClient.invalidateQueries({ queryKey: ['queue', roomId] });
    },
  });

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setSubmittedQuery(input.trim());
  };

  const results = searchQuery.data?.results ?? [];

  return (
    <div className="flex flex-col gap-3 p-4">
      <div className="grid grid-cols-2 gap-2">
        <button
          type="button"
          className={provider === ProviderName.SPOTIFY ? 'btn btn-primary' : 'btn btn-ghost'}
          onClick={() => setProvider(ProviderName.SPOTIFY)}
        >
          Spotify
        </button>
        <button
          type="button"
          className={provider === ProviderName.YOUTUBE ? 'btn btn-primary' : 'btn btn-ghost'}
          onClick={() => setProvider(ProviderName.YOUTUBE)}
        >
          YouTube
        </button>
      </div>

      <form onSubmit={onSubmit} className="flex gap-2">
        <input
          className="input"
          placeholder={`Search ${provider === ProviderName.SPOTIFY ? 'Spotify' : 'YouTube'}…`}
          value={input}
          onChange={(e) => setInput(e.target.value)}
        />
        <button
          type="submit"
          className="btn btn-primary shrink-0"
          disabled={input.trim().length === 0 || searchQuery.isFetching}
        >
          {searchQuery.isFetching ? '…' : 'Search'}
        </button>
      </form>

      {searchQuery.isError && (
        <p className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-300">
          Search failed. {(searchQuery.error as Error | null)?.message ?? ''}
        </p>
      )}

      {submittedQuery.length > 0 && !searchQuery.isFetching && results.length === 0 && (
        <p className="text-sm text-gray-400">No results for “{submittedQuery}”.</p>
      )}

      <ul className="divide-y divide-gray-800">
        {results.map((result) => {
          const added = addedRefs.has(result.externalRef);
          const adding = addToQueue.isPending && addToQueue.variables?.externalRef === result.externalRef;
          return (
            <li key={result.externalRef} className="flex items-center gap-3 py-3">
              {result.coverUrl ? (
                <img src={result.coverUrl} alt="" className="h-10 w-10 shrink-0 rounded object-cover" />
              ) : (
                <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded bg-surface-overlay text-xs text-gray-500">
                  ♪
                </div>
              )}
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm text-gray-200">{result.title}</p>
                <p className="truncate text-xs text-gray-500">
                  {result.artist || 'Unknown artist'} · {formatTime(Number(result.durationMs))}
                </p>
              </div>
              <button
                type="button"
                className="btn shrink-0 px-3 py-1.5 text-xs"
                disabled={added || adding}
                onClick={() => addToQueue.mutate(result)}
              >
                {added ? 'Added' : adding ? 'Adding…' : 'Add to queue'}
              </button>
            </li>
          );
        })}
      </ul>

      {addToQueue.isError && (
        <p className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-300">
          Could not add to queue. {(addToQueue.error as Error | null)?.message ?? ''}
        </p>
      )}
    </div>
  );
}
