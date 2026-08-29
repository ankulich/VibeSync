# ADR-0006: Synchronization clock model

- **Status:** Accepted
- **Date:** 2026-07-26
- **Phase:** 1 (Architecture; implementation in Phase 7)

## Context

VibeSync's core feature is synchronized playback: every participant in a
room must hear/see the same media position, within ~50ms, despite variable
network latency, clock skew, and host changes. Getting the *model* right is
the project's central technical challenge; the contracts must be fixed before
Playback (Phase 8), Room (Phase 6), and the frontend (Phase 11) build
against them.

## Decision

The Sync Service is the **single source of truth** for playback state. The
Playback Service only executes commands derived from it. Authoritative state
is a `SyncState` carrying a **(media_time, wall_time) pair** plus an
**epoch**:

- `media_time_ms`: position within the current media item.
- `wall_time_ms`: the server's wall clock at the moment `media_time` was
  sampled.
- `playback_rate`: 1.0 = realtime, 0.0 = paused.
- `epoch`: monotonically bumped on every authoritative change.

Clients advance locally:

```
media_time_now = state.media_time_ms
              + (client_now_ms - state.wall_time_ms) * state.playback_rate
```

This decouples "where are we" from "when did we ask," so a stale-but-recent
sample still renders correctly.

### Drift correction

Heartbeats carry the client's local `media_time` and `wall_time`. The server
computes offset and RTT (NTP-style four-timestamp exchange) per client, feeds
the offset into a **P+I controller** tuned for audio playout stability
(avoid oscillation; favor gradual correction), and broadcasts a `SyncSnapshot`
periodically with the resulting `drift_estimate_ms` and `confidence`.

### Host migration

The "host" is the client whose media source drives the authoritative clock
(e.g. the Spotify session owner). If the host leaves, the Sync Service:
1. Selects a successor (longest-present peer with provider credentials).
2. Bumps the `epoch` and the `fencing_token`.
3. Broadcasts a `HostMigration` frame.
Playback commands from the old host carry a stale fencing token and are
rejected — preventing split-brain.

### Jitter buffering

Clients maintain a small jitter buffer (target ~150ms for audio, ~400ms for
video) sized from the latest `drift_estimate_ms` and `median_rtt_ms`. The
buffer absorbs network jitter; the sync controller keeps its center aligned
to the authoritative clock.

## Consequences

- **Pros:** clean separation of authority (Sync) from execution (Playback);
  the time model degrades gracefully under packet loss; host migration is
  deterministic and fenced.
- **Cons:** the Sync Service is a critical-path dependency; if it's down,
  rooms cannot maintain sync. Mitigated by Redis-cached last-known-state for
  short outages.
- **The contracts in `proto/vibesync/sync/v1/sync.proto` are now frozen.**
  Subsequent phases must implement against them; changes require a new ADR.

## Alternatives considered

- **Master/slave per-client clock:** no single source of truth → divergent
  state. Rejected.
- **NTP-only:** assumes clocks can be corrected to wall time, which audio
  playout cannot rely on (clock drift at the DAC). The (media_time,
  wall_time) pair sidesteps this.
- **Conflict-free replicated playback (CRDT-style):** appealing but playback
  is inherently authoritative (someone pressed play); consensus is cheaper
  than merge.

The full algorithm is specified in `docs/sync/algorithm.md`.
