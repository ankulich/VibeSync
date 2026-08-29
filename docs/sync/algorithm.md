# VibeSync Synchronization Algorithm

This document specifies the algorithm the Sync Service (Phase 7) implements.
The wire contracts are fixed in `proto/vibesync/sync/v1/sync.proto`; this
document fixes the *behavior*. Implementation may not deviate from either
without a new ADR.

## Goals

- All participants in a room render the same media position within **50 ms**
  of each other (audio), **150 ms** (video), under typical residential
  network conditions.
- Survive: packet loss, client clock skew, host disconnection, host return.
- Never produce split-brain (two peers believing they're at different
  authoritative positions and unable to reconcile without a hard reset).

## Time model

Authoritative state is the `SyncState` pair `(media_time_ms, wall_time_ms)`
plus `playback_rate`. A client at wall-clock time `t_now` computes its
current media position as:

```
media_time(t_now) = state.media_time_ms
                  + (t_now - state.wall_time_ms) * state.playback_rate
```

When `playback_rate == 0` (paused), `media_time` is constant regardless of
`t_now`. This pair decouples *position* from *latency*: a sample delayed by
200 ms still renders correctly because the client recomputes from the
embedded `wall_time`.

## Heartbeat and RTT estimation

Clients send `HeartbeatRequest` at a fixed cadence (default 1 Hz, tunable
per room). Each heartbeat carries:

- `client_media_time_ms`: the client's current local media position.
- `client_wall_time_ms`: the client's wall clock at send time.
- `last_server_wall_time_ms`: the `server_wall_time_ms` from the most recent
  `HeartbeatResponse`, echoed back.

The server computes offset and RTT using a four-timestamp NTP-style
exchange:

```
Given:
  t1 = client_wall_time_ms at request send
  t2 = server wall clock at request receive
  t3 = server wall clock at response send  (≈ t2 + processing)
  t4 = client_wall_time_ms at response receive  (next heartbeat's t1)

RTT = (t4 - t1) - (t3 - t2)
offset = ((t2 - t1) + (t3 - t4)) / 2
```

`offset > 0` means the client clock is *behind* the server. The server uses
`offset` to translate the client's reported `client_media_time_ms` into
server-time terms before comparing to the authoritative `media_time_ms`,
yielding the per-client `drift_ms`.

### Smoothing

RTT and offset are smoothed via EWMA to reject transient spikes:

```
smoothed_rtt = α * rtt + (1 - α) * smoothed_rtt     // α = 0.25
smoothed_offset = α * offset + (1 - α) * smoothed_offset
```

The `HeartbeatResponse` returns `smoothed_rtt_ms` and `client_drift_ms` so
clients can adjust their jitter buffer locally.

## Drift correction (P+I controller)

The authoritative clock does not chase individual clients — that would
oscillate. Instead, the server computes a **room-wide drift signal** as the
median of active clients' drift estimates and applies a P+I controller:

```
error = median_client_drift_ms      // signed: + = clients ahead
integral += error * dt
correction = Kp * error + Ki * integral

state.media_time_ms -= correction   // nudge authoritative position
state.wall_time_ms   = now_ms       // re-sample
epoch++
```

Gains (tuned for audio playout stability, no overshoot):

- `Kp = 0.15` (proportional): gentle, prevents oscillation.
- `Ki = 0.02` (integral): clears residual steady-state error over ~30 s.
- Anti-windup: the integral term is clamped to ±200 ms.

The controller runs at 1 Hz, not per-heartbeat, so corrections are smooth.

### When NOT to correct

- If `active_peers < 2`, there's no consensus signal; skip correction.
- If `confidence < 30`, the signal is unreliable (clients just joined);
  widen jitter buffers instead.
- If `|error| > 2000 ms`, treat as a discontinuity (likely a seek the server
  missed); reset the integral and force a full snapshot.

## Jitter buffer policy

Each client maintains a playout buffer whose target depth tracks the latest
`SyncSnapshot`:

```
target_buffer_ms = clamp(
  base + 2 * smoothed_rtt + drift_estimate,
  min = 80,
  max = 600
)
```

- Audio base: 80 ms. Video base: 250 ms.
- The buffer depth is adjusted gradually (±20 ms per correction cycle) to
  avoid audible artifacts.

## Host migration

The host drives the authoritative clock's source (e.g. the Spotify session).
Migration triggers:

1. **Detection.** The host's heartbeat stops for > 5 s (configurable), or
   the host's connection drops.
2. **Selection.** The successor is the active peer with:
   (a) the longest continuous presence in the room, AND
   (b) valid provider credentials for the current media source.
   If no peer qualifies, the room is **paused** at its current position
   until a host rejoins or a member promotes themselves.
3. **Cutover.** The server:
   - bumps `epoch` (new epoch = old + 1),
   - bumps `fencing_token` (new = old + 1),
   - sets `host_id` to the successor,
   - broadcasts a `HostMigration` frame.
4. **Fencing.** The Playback Service rejects any command bearing a fencing
   token less than the current one. This prevents a partitioned old host
   (whose commands are in flight) from corrupting state.

The migration is **deterministic** given the same set of peers and presence
timestamps — all clients compute the same successor.

## Reconnect recovery

A client that lost its Subscribe stream calls `Recover(since_epoch)`. The
server:

- If `current_epoch - since_epoch <= 32`, replays the buffered frames since
  `since_epoch` from an in-memory ring buffer.
- Otherwise, returns a full `SyncSnapshot`.

The ring buffer is sized for ~10 s of frames (far longer than a typical
reconnect), bounded per room.

## Numerical targets

| Metric                         | Target | Alert at |
|--------------------------------|--------|----------|
| Median client drift            | < 30 ms | 80 ms    |
| 99th percentile client drift   | < 80 ms | 150 ms   |
| Heartbeat cadence              | 1 Hz   | < 0.5 Hz |
| Host migration cutover         | < 3 s  | 10 s     |
| Subscribe frame overhead       | < 1 KB/frame | -   |

## Non-goals

- **Bit-exact sample alignment.** We target perceptual synchrony, not sample-
  accurate phase lock. The 50 ms target reflects psychoacoustics, not
  digital audio constraints.
- **Adversarial clients.** We assume clients are non-malicious but buggy.
  Auth + fencing tokens prevent accidents, not byzantine actors; a
  misbehaving client is rate-limited and eventually disconnected.
