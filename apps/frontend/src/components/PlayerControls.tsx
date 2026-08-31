import { useEffect, useState } from 'react';
import { CommandKind, PlaybackStatus, type SyncState } from '../gen/vibesync/sync/v1/sync_pb';
import { advanceMediaTime, formatTime } from '../lib/sync';

export interface CommandOptions {
  /** Seek target in ms (CommandKind.SEEK). */
  seekToMs?: number;
  /** Playout rate 0.25..4.0 (CommandKind.SET_RATE). */
  rate?: number;
  /** Media id to load (CommandKind.LOAD_MEDIA). */
  mediaId?: string;
}

export interface PlayerControlsProps {
  syncState: SyncState | null;
  /** Sends a playback command; the caller attaches the current fencingToken. */
  onCommand: (kind: CommandKind, opts?: CommandOptions) => void;
  /** Title of the queue's current media, when known. */
  mediaTitle?: string | null;
  /** Duration of the current media in ms, when known. */
  mediaDurationMs?: number | null;
  /**
   * Whether the local user may issue commands. The sync protocol only
   * accepts commands from the host; false disables the transport UI so
   * non-hosts are not left pressing buttons that silently fail.
   */
  controlsEnabled?: boolean;
}

/** Re-renders on an interval so the projected position keeps ticking. */
const POSITION_TICK_MS = 250;

export default function PlayerControls({
  syncState,
  onCommand,
  mediaTitle,
  mediaDurationMs,
  controlsEnabled = true,
}: PlayerControlsProps) {
  // Ticker only exists to force re-renders; the value itself is unused.
  const [, setTick] = useState(0);
  const [scrubMs, setScrubMs] = useState<number | null>(null);

  useEffect(() => {
    const id = window.setInterval(() => setTick((t) => t + 1), POSITION_TICK_MS);
    return () => window.clearInterval(id);
  }, []);

  if (!syncState) {
    return (
      <div className="card flex items-center justify-center py-12 text-sm text-gray-400">
        Connecting to the room clock…
      </div>
    );
  }

  const isPlaying = syncState.status === PlaybackStatus.PLAYING;
  const playoutRate = isPlaying ? syncState.playbackRate : 0;
  const positionMs = advanceMediaTime(
    Number(syncState.mediaTimeMs),
    Number(syncState.wallTimeMs),
    playoutRate,
  );
  const durationMs = mediaDurationMs ?? 0;
  const maxMs = durationMs > 0 ? durationMs : Math.max(positionMs, 1_000);
  const sliderValue = Math.min(scrubMs ?? positionMs, maxMs);
  const seekEnabled = controlsEnabled && (durationMs > 0 || positionMs > 0);

  const commitScrub = () => {
    if (scrubMs == null) return;
    onCommand(CommandKind.SEEK, { seekToMs: scrubMs });
    setScrubMs(null);
  };

  return (
    <div className="card space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <p className="truncate text-lg font-semibold">{mediaTitle ?? 'No media loaded'}</p>
          <p className="mt-0.5 text-xs text-gray-500">
            {PlaybackStatus[syncState.status]?.toLowerCase() ?? 'unknown'} · rate{' '}
            {syncState.playbackRate.toFixed(2)}x · epoch {syncState.epoch.toString()}
          </p>
        </div>
        <span
          className={`shrink-0 rounded-full px-2.5 py-1 text-xs font-medium ${
            isPlaying ? 'bg-emerald-500/15 text-emerald-300' : 'bg-gray-500/20 text-gray-300'
          }`}
        >
          {isPlaying ? 'Playing' : 'Paused'}
        </span>
      </div>

      <div>
        <input
          type="range"
          min={0}
          max={maxMs}
          step={500}
          value={sliderValue}
          disabled={!seekEnabled}
          onChange={(e) => setScrubMs(Number(e.target.value))}
          onPointerUp={() => commitScrub()}
          onKeyUp={() => commitScrub()}
          className="w-full"
          aria-label="Seek"
        />
        <div className="flex justify-between text-xs tabular-nums text-gray-400">
          <span>{formatTime(positionMs)}</span>
          <span>{durationMs > 0 ? formatTime(durationMs) : '--:--'}</span>
        </div>
      </div>

      <div className="flex items-center justify-center gap-3">
        <button
          type="button"
          className="btn btn-ghost"
          disabled={!controlsEnabled}
          onClick={() => onCommand(CommandKind.PREVIOUS)}
        >
          Prev
        </button>
        {isPlaying ? (
          <button
            type="button"
            className="btn btn-primary px-8"
            disabled={!controlsEnabled}
            onClick={() => onCommand(CommandKind.PAUSE)}
          >
            Pause
          </button>
        ) : (
          <button
            type="button"
            className="btn btn-primary px-8"
            disabled={!controlsEnabled}
            onClick={() => onCommand(CommandKind.PLAY)}
          >
            Play
          </button>
        )}
        <button
          type="button"
          className="btn btn-ghost"
          disabled={!controlsEnabled}
          onClick={() => onCommand(CommandKind.NEXT)}
        >
          Next
        </button>
      </div>

      {!controlsEnabled && (
        <p className="text-center text-xs text-gray-500">
          Playback is controlled by the host — you watch in sync automatically.
        </p>
      )}
    </div>
  );
}
