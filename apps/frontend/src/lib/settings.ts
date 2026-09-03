/**
 * Runtime-adjustable client settings (ADR-0017).
 *
 * No magic numbers in player code: every tunable lives here as a named,
 * documented field. The loader composes two layers today:
 *
 *   1. DEFAULT_SETTINGS — compiled-in defaults, the single source of truth.
 *   2. localStorage ("vibesync.settings") — operator/user overrides.
 *
 * The shape is intentionally transport-agnostic: when the future admin
 * panel ships, a third layer (fetched from a backend settings endpoint) can
 * be merged in `loadSettings()` without touching any consumer.
 */

export interface SyncPlayerSettings {
  /**
   * Acceptable drift deadband (ms): below this the player never corrects.
   * Product rule (ADR-0017): a 0.3–0.5 s difference between a guest and
   * the owner is acceptable — constant chasing is worse than the drift.
   */
  driftIgnoreMs: number;
  /**
   * Hard resync threshold (ms): beyond this the player seeks back to the
   * authoritative position. Between driftIgnoreMs and driftResyncMs the
   * drift is tolerated (visible, but harmless and stable).
   */
  driftResyncMs: number;
  /** How often the sync loop reconciles the player with the projection. */
  syncTickMs: number;
}

export interface AppSettings {
  /** Synchronized-player tuning. */
  syncPlayer: SyncPlayerSettings;
}

export const DEFAULT_SETTINGS: AppSettings = {
  syncPlayer: {
    driftIgnoreMs: 300,
    driftResyncMs: 500,
    syncTickMs: 250,
  },
};

const STORAGE_KEY = "vibesync.settings";

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** Merges partial overrides onto the defaults, dropping unknown keys. */
function merge<T extends object>(base: T, override: unknown): T {
  if (!isPlainObject(override)) return base;
  const baseRecord = base as unknown as Record<string, unknown>;
  const out: Record<string, unknown> = { ...baseRecord };
  for (const [k, v] of Object.entries(override)) {
    if (k in baseRecord && typeof v === typeof baseRecord[k]) {
      out[k] = v;
    }
  }
  return out as T;
}

/**
 * Loads the effective settings. Consumers call this once per session (or
 * after a settings-change event); it is cheap and synchronous today and
 * ready to become async/fetched when the admin panel lands.
 */
export function loadSettings(): AppSettings {
  let stored: unknown = null;
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) stored = JSON.parse(raw);
  } catch {
    // Corrupt overrides fall back to defaults.
  }
  if (!isPlainObject(stored)) return DEFAULT_SETTINGS;
  return {
    syncPlayer: merge(DEFAULT_SETTINGS.syncPlayer, (stored as Record<string, unknown>).syncPlayer),
  };
}
