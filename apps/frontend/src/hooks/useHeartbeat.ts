import { useEffect, useRef, useState } from 'react';
import { getSyncClient } from '../api/clients';

export interface UseHeartbeatOptions {
  roomId: string;
  /** Returns the client's current media position in ms (projected via advanceMediaTime). */
  getMediaTimeMs: () => number;
}

export interface UseHeartbeatResult {
  /** Smoothed round-trip estimate from the latest response, in ms. */
  smoothedRttMs: number | null;
  /** Drift vs the authoritative clock from the latest response, in ms. */
  clientDriftMs: number | null;
}

const HEARTBEAT_INTERVAL_MS = 1_000;

/**
 * Sends a heartbeat once per second while a room is open. Each request echoes
 * the serverWallTimeMs of the previous response so the server can estimate RTT
 * without trusting the client clock for ordering; the response carries back the
 * smoothed RTT and this client's drift estimate, surfaced for the SyncIndicator.
 */
export function useHeartbeat({ roomId, getMediaTimeMs }: UseHeartbeatOptions): UseHeartbeatResult {
  const [smoothedRttMs, setSmoothedRttMs] = useState<number | null>(null);
  const [clientDriftMs, setClientDriftMs] = useState<number | null>(null);
  const lastServerWallTimeRef = useRef<bigint>(0n);
  const inFlightRef = useRef(false);

  useEffect(() => {
    if (!roomId) return;

    const syncClient = getSyncClient();
    const roomIdMessage = { value: roomId };

    const interval = window.setInterval(() => {
      if (inFlightRef.current) return; // never pile up slow heartbeats
      inFlightRef.current = true;
      void (async () => {
        try {
          const response = await syncClient.heartbeat({
            roomId: roomIdMessage,
            clientMediaTimeMs: BigInt(Math.max(0, Math.round(getMediaTimeMs()))),
            clientWallTimeMs: BigInt(Date.now()),
            lastServerWallTimeMs: lastServerWallTimeRef.current,
          });
          lastServerWallTimeRef.current = response.serverWallTimeMs;
          setSmoothedRttMs(response.smoothedRttMs);
          setClientDriftMs(response.clientDriftMs);
        } catch {
          // Heartbeats are best-effort; the sync stream owns correctness/reconnect.
        } finally {
          inFlightRef.current = false;
        }
      })();
    }, HEARTBEAT_INTERVAL_MS);

    return () => {
      window.clearInterval(interval);
    };
  }, [roomId, getMediaTimeMs]);

  return { smoothedRttMs, clientDriftMs };
}
