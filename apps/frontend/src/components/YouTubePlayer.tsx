import { useCallback, useEffect, useRef, useState } from 'react';
import { PlaybackStatus, type SyncState } from '../gen/vibesync/sync/v1/sync_pb';
import { advanceMediaTime } from '../lib/sync';

export interface YouTubePlayerProps {
  /** YouTube video id (media.externalRef for provider-sourced video). */
  videoId: string;
  /** Authoritative room state; drives play/pause/seek/rate. */
  syncState: SyncState | null;
  /** Reports the video duration (ms) once the player knows it. */
  onDuration?: (durationMs: number) => void;
}

/** How often the sync loop compares the player against the projected position. */
const SYNC_TICK_MS = 250;
/** Beyond this drift (ms) the player is hard-corrected with a seek. */
const HARD_SEEK_DRIFT_MS = 750;
/** Below this |drift| (ms) nothing is corrected — imperceptible and stable. */
const SOFT_DRIFT_FLOOR_MS = 150;
/** Playback rates the IFrame player accepts (per API docs). */
const SUPPORTED_RATES = [0.25, 0.5, 0.75, 1, 1.25, 1.5, 1.75, 2];

/** IFrame API error codes → readable messages. */
const PLAYER_ERRORS: Record<number, string> = {
  2: 'Invalid video id.',
  5: 'The video could not be played in this browser.',
  100: 'Video not found or is private.',
  101: 'The owner does not allow this video to be embedded.',
  150: 'The owner does not allow this video to be embedded.',
};

/** Loads the IFrame API script once per page; resolves with the YT namespace. */
let iframeApiPromise: Promise<typeof YT> | null = null;
function loadIframeApi(): Promise<typeof YT> {
  if (iframeApiPromise) return iframeApiPromise;
  iframeApiPromise = new Promise<typeof YT>((resolve, reject) => {
    if (typeof window === 'undefined') {
      reject(new Error('YouTube IFrame API requires a browser'));
      return;
    }
    if (window.YT?.Player) {
      resolve(window.YT);
      return;
    }
    const previous = window.onYouTubeIframeAPIReady;
    window.onYouTubeIframeAPIReady = () => {
      previous?.();
      resolve(window.YT!);
    };
    const script = document.createElement('script');
    script.src = 'https://www.youtube.com/iframe_api';
    script.async = true;
    script.onerror = () => reject(new Error('Failed to load the YouTube IFrame API'));
    document.head.appendChild(script);
  });
  return iframeApiPromise;
}

/** Snaps a requested rate to the closest rate the player supports. */
function snapRate(rate: number): number {
  return SUPPORTED_RATES.reduce((best, r) =>
    Math.abs(r - rate) < Math.abs(best - rate) ? r : best,
  );
}

/**
 * The synchronized YouTube player: a plain IFrame embed whose transport is
 * driven by the room's authoritative SyncState (docs/sync/algorithm.md).
 * Native player controls stay visible so every viewer picks quality and
 * subtitles locally — these are per-user and never affect the shared clock.
 */
export default function YouTubePlayer({ videoId, syncState, onDuration }: YouTubePlayerProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const playerRef = useRef<YT.Player | null>(null);
  const syncStateRef = useRef<SyncState | null>(null);
  const onDurationRef = useRef<((ms: number) => void) | undefined>(onDuration);
  const gestureConsumedRef = useRef(false);
  const lastAppliedEpochRef = useRef<bigint | null>(null);
  const reportedDurationRef = useRef(0);

  const [playerReady, setPlayerReady] = useState(false);
  const [playerState, setPlayerState] = useState<number>(-1);
  const [needsGesture, setNeedsGesture] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    syncStateRef.current = syncState;
  }, [syncState]);
  useEffect(() => {
    onDurationRef.current = onDuration;
  }, [onDuration]);

  // Create the player once; the container div is replaced by the iframe.
  useEffect(() => {
    if (!containerRef.current) return;
    let destroyed = false;

    void loadIframeApi()
      .then((YT) => {
        if (destroyed || !containerRef.current) return;
        playerRef.current = new YT.Player(containerRef.current, {
          videoId,
          playerVars: {
            controls: 1,
            disablekb: 1,
            modestbranding: 1,
            rel: 0,
            playsinline: 1,
            origin: window.location.origin,
          },
          events: {
            onReady: () => {
              if (destroyed) return;
              setPlayerReady(true);
            },
            onStateChange: (event) => {
              if (destroyed) return;
              setPlayerState(event.data);
              if (event.data === YT.PlayerState.PLAYING) {
                // Autoplay worked (or the user already interacted).
                gestureConsumedRef.current = true;
                setNeedsGesture(false);
              }
              const durationSec = playerRef.current?.getDuration() ?? 0;
              if (durationSec > 0 && durationSec !== reportedDurationRef.current) {
                reportedDurationRef.current = durationSec;
                onDurationRef.current?.(durationSec * 1000);
              }
            },
            onError: (event) => {
              if (destroyed) return;
              const code = typeof event.data === 'number' ? event.data : 0;
              setError(PLAYER_ERRORS[code] ?? `Playback error (${code}).`);
            },
          },
        });
      })
      .catch((err: unknown) => {
        if (!destroyed) setError((err as Error | null)?.message ?? 'Failed to load the player.');
      });

    return () => {
      destroyed = true;
      try {
        playerRef.current?.destroy();
      } catch {
        // The iframe may already be gone.
      }
      playerRef.current = null;
      setPlayerReady(false);
    };
    // The player is created for the mount; media swaps go through loadVideo.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Media swap: cue the new video; the sync loop decides whether to play it.
  useEffect(() => {
    if (!playerReady || !playerRef.current) return;
    setError(null);
    reportedDurationRef.current = 0;
    onDurationRef.current?.(0);
    playerRef.current.cueVideoById(videoId);
  }, [playerReady, videoId]);

  /** Projects the authoritative position to "now", in seconds. */
  const targetTimeSec = useCallback((): number | null => {
    const state = syncStateRef.current;
    if (!state) return null;
    const rate = state.status === PlaybackStatus.PLAYING ? state.playbackRate : 0;
    return advanceMediaTime(Number(state.mediaTimeMs), Number(state.wallTimeMs), rate) / 1000;
  }, []);

  // The sync loop: reconcile the local iframe with the authoritative state.
  useEffect(() => {
    if (!playerReady) return;

    const tick = () => {
      const player = playerRef.current;
      const state = syncStateRef.current;
      if (!player || !state) return;

      const target = targetTimeSec();
      if (target == null) return;
      const playing = state.status === PlaybackStatus.PLAYING;
      const localState = player.getPlayerState();
      const current = player.getCurrentTime();
      const driftMs = (current - target) * 1000;

      // A new epoch means an authoritative discontinuity (command, migration,
      // snapshot): re-anchor immediately instead of waiting for drift.
      const epoch = state.epoch;
      const reanchor = lastAppliedEpochRef.current !== epoch;
      lastAppliedEpochRef.current = epoch;

      // Once the video has ENDED locally, stop chasing the authoritative
      // clock: it keeps advancing past the duration, so drift-based seeks
      // would rewind the player into the last seconds over and over (the
      // video appears to loop). An epoch change (host seek/load/pause)
      // re-arms the chase so viewers follow the host again.
      if (localState === 0 /* ENDED */ && !reanchor) {
        return;
      }

      if (playing) {
        // Browsers block unmuted autoplay until a user gesture; surface the
        // join overlay instead of fighting the policy in a loop.
        const blocked =
          localState === -1 || localState === 2 /* PAUSED */ || localState === 5 /* CUED */;
        if (blocked && !gestureConsumedRef.current) {
          player.playVideo(); // may be silently rejected — overlay covers it
          setNeedsGesture(true);
          return;
        }
        if (blocked) {
          player.playVideo();
        }
      } else {
        if (localState === 1 /* PLAYING */ || localState === 3 /* BUFFERING */) {
          player.pauseVideo();
        }
      }

      // Apply the authoritative rate, snapped to the player's discrete steps.
      const wantedRate = snapRate(state.playbackRate);
      if (player.getPlaybackRate() !== wantedRate) player.setPlaybackRate(wantedRate);

      // Convergence: hard-seek on large drift, or right after an
      // authoritative discontinuity (new epoch). Between the floor and the
      // threshold the drift is imperceptible — YT rates are too coarse for
      // gentler correction, so we let it ride.
      const needsSeek =
        Math.abs(driftMs) > HARD_SEEK_DRIFT_MS ||
        (reanchor && Math.abs(driftMs) > SOFT_DRIFT_FLOOR_MS);
      if (needsSeek) {
        player.seekTo(target, true);
      }
    };

    tick();
    const id = window.setInterval(tick, SYNC_TICK_MS);
    return () => window.clearInterval(id);
  }, [playerReady, targetTimeSec]);

  /** User-gesture unlock for autoplay: click joins synchronized playback. */
  const joinPlayback = () => {
    gestureConsumedRef.current = true;
    setNeedsGesture(false);
    const player = playerRef.current;
    if (!player) return;
    player.unMute();
    player.setVolume(100);
    if (syncStateRef.current?.status === PlaybackStatus.PLAYING) {
      player.playVideo();
    }
  };

  const buffering = playerState === 3;

  return (
    <div className="card overflow-hidden p-0">
      <div className="relative aspect-video w-full bg-black">
        <div ref={containerRef} className="absolute inset-0 h-full w-full" />

        {needsGesture && !error && (
          <button
            type="button"
            onClick={joinPlayback}
            className="absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-black/70 text-center backdrop-blur-sm"
          >
            <span className="flex h-16 w-16 items-center justify-center rounded-full bg-accent text-2xl text-white shadow-lg transition-transform hover:scale-105">
              ▶
            </span>
            <span className="text-sm font-medium text-gray-100">Join synchronized playback</span>
            <span className="text-xs text-gray-400">
              Browsers need one click before video can start with sound
            </span>
          </button>
        )}

        {error && (
          <div className="absolute inset-0 z-20 flex flex-col items-center justify-center gap-2 bg-black/85 px-6 text-center">
            <p className="text-sm font-medium text-red-300">{error}</p>
            <p className="text-xs text-gray-400">
              Pick another video from the queue, or add one by URL in the sidebar.
            </p>
          </div>
        )}

        {buffering && !error && (
          <div className="pointer-events-none absolute right-3 top-3 z-10 rounded-full bg-black/70 px-3 py-1 text-xs text-gray-200">
            Buffering…
          </div>
        )}
      </div>
    </div>
  );
}
