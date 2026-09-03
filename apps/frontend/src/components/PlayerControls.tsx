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
  /** Duration of the current media in ms, when known. */
  mediaDurationMs?: number | null;
  /**
   * Granular control grants (ADR-0017). The host/owner implicitly holds
   * all; a guest holds exactly what the owner granted. Undefined = all
   * allowed (backwards-compatible default).
   */
  canSeek?: boolean;
  canPlayPause?: boolean;
  canSwitch?: boolean;
}

/** Re-renders on an interval so the projected position keeps ticking. */
const POSITION_TICK_MS = 250;

/**
 * The room transport, rendered compactly in the page header: prev,
 * play/pause, seek slider, next, and the position/duration readout.
 * Grants follow ADR-0017 — controls without permission render disabled
 * with an explanatory tooltip.
 */
export default function PlayerControls({
  syncState,
  onCommand,
  mediaDurationMs,
  canSeek = true,
  canPlayPause = true,
  canSwitch = true,
}: PlayerControlsProps) {
  // Ticker only exists to force re-renders; the value itself is unused.
  const [, setTick] = useState(0);
  const [scrubMs, setScrubMs] = useState<number | null>(null);

  useEffect(() => {
    const id = window.setInterval(() => setTick((t) => t + 1), POSITION_TICK_MS);
    return () => window.clearInterval(id);
  }, []);

  if (!syncState) {
    return <span className="text-xs text-gray-500">Connecting to the room clock…</span>;
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
  const seekEnabled = canSeek && (durationMs > 0 || positionMs > 0);
  const nothingAllowed = !canSeek && !canPlayPause && !canSwitch;
  const stateLabel = `${PlaybackStatus[syncState.status]?.toLowerCase() ?? 'unknown'} · rate ${syncState.playbackRate.toFixed(2)}x · epoch ${syncState.epoch.toString()}`;

  const commitScrub = () => {
    if (scrubMs == null) return;
    onCommand(CommandKind.SEEK, { seekToMs: scrubMs });
    setScrubMs(null);
  };

  return (
    <div
      className="flex w-full max-w-3xl min-w-0 items-center gap-2"
      title={stateLabel}
      role="group"
      aria-label="Room playback transport"
    >
      <button
        type="button"
        className="btn btn-ghost shrink-0 px-3 py-1.5 text-xs"
        disabled={!canSwitch}
        onClick={() => onCommand(CommandKind.PREVIOUS)}
        aria-label="Previous"
        title={canSwitch ? 'Previous' : 'Switching requires the owner to grant permission'}
      >
        Prev
      </button>
      {isPlaying ? (
        <button
          type="button"
          className="btn btn-primary shrink-0 px-6 py-1.5 text-xs"
          disabled={!canPlayPause}
          onClick={() => onCommand(CommandKind.PAUSE)}
          aria-label="Pause"
          title={canPlayPause ? 'Pause' : 'Pause requires the owner to grant permission'}
        >
          Pause
        </button>
      ) : (
        <button
          type="button"
          className="btn btn-primary shrink-0 px-6 py-1.5 text-xs"
          disabled={!canPlayPause}
          onClick={() => onCommand(CommandKind.PLAY)}
          aria-label="Play"
          title={canPlayPause ? 'Play' : 'Starting playback requires the owner to grant permission'}
        >
          Play
        </button>
      )}
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
        className="min-w-[14rem] flex-1 sm:min-w-[20rem]"
        aria-label="Seek"
        title={seekEnabled ? undefined : 'Seeking requires the owner to grant permission'}
      />
      <span className="shrink-0 whitespace-nowrap text-xs tabular-nums text-gray-400">
        {formatTime(scrubMs ?? positionMs)}
        {durationMs > 0 ? ` / ${formatTime(durationMs)}` : ''}
      </span>
      <button
        type="button"
        className="btn btn-ghost shrink-0 px-3 py-1.5 text-xs"
        disabled={!canSwitch}
        onClick={() => onCommand(CommandKind.NEXT)}
        aria-label="Next"
        title={canSwitch ? 'Next' : 'Switching requires the owner to grant permission'}
      >
        Next
      </button>
      {nothingAllowed && (
        <span className="shrink-0 text-xs text-gray-500" title="Playback is controlled by the host — you watch in sync automatically.">
          🔒
        </span>
      )}
    </div>
  );
}
