import { useCallback, useEffect, useRef, useState } from 'react';
import { getSyncClient } from '../api/clients';
import { advanceMediaTime } from '../lib/sync';
import type {
  HostMigration,
  SubscribeResponse,
  SyncSnapshot,
  SyncState,
} from '../gen/vibesync/sync/v1/sync_pb';
import { PlaybackStatus } from '../gen/vibesync/sync/v1/sync_pb';

export type ConnectionStatus = 'connecting' | 'connected' | 'error';

export interface UseSyncStreamResult {
  /** Latest authoritative state accepted by epoch fencing. */
  syncState: SyncState | null;
  connectionStatus: ConnectionStatus;
  /** Projects a SyncState's media position to "now" on the client clock. */
  advanceMediaTime: (state: SyncState | null) => number;
}

const BASE_BACKOFF_MS = 1_000;
const MAX_BACKOFF_MS = 15_000;
const MAX_BACKOFF_EXPONENT = 4;

/**
 * THE CORE HOOK: subscribes to the room's authoritative sync stream, applies
 * frames with epoch fencing, reacts to host migrations, and recovers after
 * disconnects via the Recover RPC before reconnecting with backoff.
 */
export function useSyncStream(roomId: string): UseSyncStreamResult {
  const [syncState, setSyncState] = useState<SyncState | null>(null);
  const [connectionStatus, setConnectionStatus] = useState<ConnectionStatus>('connecting');
  const lastEpochRef = useRef<bigint>(0n);
  const stateRef = useRef<SyncState | null>(null);

  useEffect(() => {
    if (!roomId) return;

    // Reset per-room bookkeeping (also covers roomId changes).
    lastEpochRef.current = 0n;
    stateRef.current = null;
    setSyncState(null);
    setConnectionStatus('connecting');

    let cancelled = false;
    let attempt = 0;
    let reconnectTimer: number | undefined;
    let activeIterator: AsyncIterator<SubscribeResponse> | null = null;

    const roomIdMessage = { value: roomId };

    /** Applies an authoritative state if it is not older than what we have. */
    const applyState = (state: SyncState): void => {
      if (state.epoch >= lastEpochRef.current) {
        lastEpochRef.current = state.epoch;
        stateRef.current = state;
        setSyncState(state);
      }
    };

    const applySnapshot = (snapshot: SyncSnapshot): void => {
      if (snapshot.state) applyState(snapshot.state);
    };

    /** Host migration: re-stamp host + fencing token on the current state. */
    const applyMigration = (migration: HostMigration): void => {
      const prev = stateRef.current;
      if (!prev) return;
      if (migration.newEpoch < prev.epoch) return; // stale migration notice
      const next = prev.clone();
      if (migration.newHostId) next.hostId = migration.newHostId;
      next.fencingToken = migration.newFencingToken;
      next.epoch = migration.newEpoch;
      lastEpochRef.current = migration.newEpoch;
      stateRef.current = next;
      setSyncState(next);
    };

    const handleFrame = (frame: SubscribeResponse): void => {
      switch (frame.payload.case) {
        case 'update':
          applyState(frame.payload.value);
          break;
        case 'snapshot':
          applySnapshot(frame.payload.value);
          break;
        case 'hostMigration':
          applyMigration(frame.payload.value);
          break;
        default:
          break;
      }
    };

    const run = async (): Promise<void> => {
      const syncClient = getSyncClient();

      while (!cancelled) {
        setConnectionStatus('connecting');
        try {
          const stream = syncClient.subscribe({
            roomId: roomIdMessage,
            lastAppliedEpoch: lastEpochRef.current,
          });
          const iterator = stream[Symbol.asyncIterator]();
          activeIterator = iterator;
          try {
            for (;;) {
              const result = await iterator.next();
              if (result.done || cancelled) break;
              attempt = 0;
              setConnectionStatus('connected');
              handleFrame(result.value);
            }
            // Server closed the stream cleanly — fall through to recovery.
          } finally {
            activeIterator = null;
            try {
              await iterator.return?.();
            } catch {
              // Already terminated — nothing to release.
            }
          }
        } catch {
          // Stream errored — recover below unless we are unmounting.
        }
        if (cancelled) return;

        // The stream ended or failed: ask the server for what we missed.
        try {
          const recovery = await syncClient.recover({
            roomId: roomIdMessage,
            sinceEpoch: lastEpochRef.current,
          });
          if (!cancelled) {
            if (recovery.payload.case === 'snapshot') {
              applySnapshot(recovery.payload.value);
            } else if (recovery.payload.case === 'frames') {
              for (const frame of recovery.payload.value.frames) {
                handleFrame(frame);
              }
            }
          }
        } catch {
          if (cancelled) return;
          setConnectionStatus('error');
        }

        // Reconnect with exponential backoff.
        const delay = Math.min(
          BASE_BACKOFF_MS * 2 ** Math.min(attempt, MAX_BACKOFF_EXPONENT),
          MAX_BACKOFF_MS,
        );
        attempt += 1;
        await new Promise<void>((resolve) => {
          reconnectTimer = window.setTimeout(resolve, delay);
        });
      }
    };

    void run();

    return () => {
      cancelled = true;
      if (reconnectTimer !== undefined) {
        window.clearTimeout(reconnectTimer);
      }
      // Aborts the in-flight HTTP request behind the stream, if any.
      void activeIterator?.return?.().catch(() => undefined);
    };
  }, [roomId]);

  const advance = useCallback((state: SyncState | null): number => {
    if (!state) return 0;
    const rate = state.status === PlaybackStatus.PLAYING ? state.playbackRate : 0;
    return advanceMediaTime(Number(state.mediaTimeMs), Number(state.wallTimeMs), rate);
  }, []);

  return { syncState, connectionStatus, advanceMediaTime: advance };
}
