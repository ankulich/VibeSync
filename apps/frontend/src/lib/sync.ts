/**
 * Client-side clock math from docs/sync/algorithm.md.
 *
 * The server publishes a (media_time_ms, wall_time_ms, playback_rate) tuple;
 * between frames the client projects the position forward using its own
 * wall clock. `wallTimeMs` is the *server* wall-clock at which the media time
 * was sampled, so a roughly synchronized client clock yields sub-frame
 * accuracy, and heartbeats keep the residual drift measured and bounded.
 */

/**
 * Projects an authoritative media position to "now".
 *
 * @param mediaTimeMs  Media position in milliseconds at the sampled wall time.
 * @param wallTimeMs   Server wall-clock (ms since Unix epoch) when mediaTimeMs was sampled.
 * @param playbackRate Playout rate; 0 (paused) freezes the position.
 * @returns The projected media position in milliseconds.
 */
export function advanceMediaTime(mediaTimeMs: number, wallTimeMs: number, playbackRate: number): number {
  if (playbackRate === 0) return mediaTimeMs;
  const now = Date.now();
  const elapsed = Math.max(0, now - wallTimeMs);
  return mediaTimeMs + elapsed * playbackRate;
}

/**
 * Formats a duration in milliseconds as "3:45" (or "1:02:03" for hour+ media).
 */
export function formatTime(ms: number): string {
  const safe = Number.isFinite(ms) && ms > 0 ? Math.floor(ms) : 0;
  const totalSeconds = Math.floor(safe / 1000);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  const ss = String(seconds).padStart(2, '0');
  if (hours > 0) {
    return `${hours}:${String(minutes).padStart(2, '0')}:${ss}`;
  }
  return `${minutes}:${ss}`;
}
