import type { ConnectionStatus } from '../hooks/useSyncStream';

export interface SyncIndicatorProps {
  driftMs: number | null;
  rttMs: number | null;
  connectionStatus: ConnectionStatus;
}

const DOT_CLASS: Record<ConnectionStatus, string> = {
  connected: 'bg-emerald-500',
  connecting: 'bg-yellow-400 animate-pulse',
  error: 'bg-red-500',
};

export default function SyncIndicator({ driftMs, rttMs, connectionStatus }: SyncIndicatorProps) {
  const driftLabel = driftMs == null ? '±—ms' : `±${Math.abs(Math.round(driftMs))}ms`;
  const rttLabel = rttMs == null ? '—ms RTT' : `${Math.round(rttMs)}ms RTT`;

  return (
    <div className="flex items-center gap-2.5 rounded-full bg-surface-raised px-3 py-1.5 text-xs text-gray-300">
      <span className={`h-2 w-2 shrink-0 rounded-full ${DOT_CLASS[connectionStatus]}`} />
      <span className="capitalize">{connectionStatus}</span>
      <span className="text-gray-600">|</span>
      <span className="tabular-nums" title="Drift vs authoritative clock">
        {driftLabel}
      </span>
      <span className="text-gray-600">|</span>
      <span className="tabular-nums" title="Smoothed round-trip time">
        {rttLabel}
      </span>
    </div>
  );
}
