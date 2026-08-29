import { create } from 'zustand';
import { getAuthClient } from '../api/clients';
import { setAuthContextGetter } from '../api/transport';
import type { AuthSession } from '../gen/vibesync/auth/v1/auth_pb';
import { SystemRole } from '../gen/vibesync/common/v1/common_pb';

const STORAGE_KEY = 'vibesync.auth';

/** sessionStorage key holding { provider, state, returnTo? } between BeginOAuth and the callback page. */
export const OAUTH_SESSION_KEY = 'vibesync.oauth';

interface PersistedAuth {
  accessToken: string;
  refreshToken: string;
  userId: string;
  systemRole: SystemRole;
}

function loadPersistedAuth(): PersistedAuth | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<PersistedAuth>;
    if (
      typeof parsed.accessToken !== 'string' ||
      typeof parsed.refreshToken !== 'string' ||
      !parsed.accessToken ||
      !parsed.refreshToken
    ) {
      return null;
    }
    return {
      accessToken: parsed.accessToken,
      refreshToken: parsed.refreshToken,
      userId: typeof parsed.userId === 'string' ? parsed.userId : '',
      systemRole: typeof parsed.systemRole === 'number' ? parsed.systemRole : SystemRole.USER,
    };
  } catch {
    return null;
  }
}

function persistAuth(auth: PersistedAuth | null): void {
  try {
    if (auth) {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(auth));
    } else {
      localStorage.removeItem(STORAGE_KEY);
    }
  } catch {
    // Storage unavailable (private mode, quota) — the session just won't survive a reload.
  }
}

function sessionToAuth(session: AuthSession | undefined): PersistedAuth | null {
  const accessToken = session?.tokens?.accessToken ?? '';
  const refreshToken = session?.tokens?.refreshToken ?? '';
  if (!accessToken || !refreshToken) return null;
  return {
    accessToken,
    refreshToken,
    userId: session?.userId?.value ?? '',
    systemRole: SystemRole.USER,
  };
}

export interface AuthState {
  accessToken: string | null;
  refreshToken: string | null;
  userId: string | null;
  systemRole: SystemRole;
  isAuthenticated: boolean;

  /** Password login; stores the returned token pair + user id. */
  login: (email: string, password: string) => Promise<void>;
  /** Password registration; creates the account and stores the returned token pair + user id. */
  register: (email: string, username: string, password: string) => Promise<void>;
  /** Exchanges an OAuth authorization code for a session. */
  oauthComplete: (provider: string, code: string, state: string) => Promise<void>;
  /** Revokes the refresh token server-side and clears local state. */
  logout: () => Promise<void>;
  /** Rotates the access token using the stored (single-use) refresh token. */
  refreshTokens: () => Promise<boolean>;
  /** Direct token injection (used by the OAuth callback before navigating on). */
  setTokens: (accessToken: string, refreshToken: string, userId?: string) => void;
}

export const useAuthStore = create<AuthState>()((set, get) => {
  const persisted = loadPersistedAuth();

  function applyAuth(auth: PersistedAuth): void {
    persistAuth(auth);
    set({
      accessToken: auth.accessToken,
      refreshToken: auth.refreshToken,
      userId: auth.userId || get().userId,
      systemRole: auth.systemRole,
      isAuthenticated: true,
    });
  }

  function clearAuth(): void {
    persistAuth(null);
    set({
      accessToken: null,
      refreshToken: null,
      userId: null,
      systemRole: SystemRole.UNSPECIFIED,
      isAuthenticated: false,
    });
  }

  return {
    accessToken: persisted?.accessToken ?? null,
    refreshToken: persisted?.refreshToken ?? null,
    userId: persisted?.userId || null,
    systemRole: persisted?.systemRole ?? SystemRole.USER,
    isAuthenticated: persisted != null,

    login: async (email, password) => {
      const response = await getAuthClient().login({
        email,
        password,
        deviceLabel: 'VibeSync Web',
      });
      const auth = sessionToAuth(response.session);
      if (!auth) throw new Error('Login response did not include a token pair');
      applyAuth(auth);
    },

    register: async (email, username, password) => {
      const response = await getAuthClient().register({
        email,
        username,
        password,
        deviceLabel: 'VibeSync Web',
      });
      const auth = sessionToAuth(response.session);
      if (!auth) throw new Error('Register response did not include a token pair');
      applyAuth(auth);
    },

    oauthComplete: async (provider, code, state) => {
      const response = await getAuthClient().completeOAuth({ provider, code, state });
      const auth = sessionToAuth(response.session);
      if (!auth) throw new Error('OAuth response did not include a token pair');
      applyAuth(auth);
    },

    logout: async () => {
      const { refreshToken } = get();
      if (refreshToken) {
        try {
          await getAuthClient().logout({ refreshToken });
        } catch {
          // Best effort — clear locally regardless of server outcome.
        }
      }
      clearAuth();
    },

    refreshTokens: async () => {
      const { refreshToken } = get();
      if (!refreshToken) return false;
      try {
        const response = await getAuthClient().refreshToken({ refreshToken });
        const auth = sessionToAuth(response.session);
        if (!auth) throw new Error('empty session');
        applyAuth(auth);
        return true;
      } catch {
        clearAuth();
        return false;
      }
    },

    setTokens: (accessToken, refreshToken, userId) => {
      applyAuth({
        accessToken,
        refreshToken,
        userId: userId ?? get().userId ?? '',
        systemRole: get().systemRole,
      });
    },
  };
});

// Feed the transport's auth interceptor from the store. The getter reads fresh
// state on every request, so token rotations apply to in-flight usage immediately.
setAuthContextGetter(() => {
  const state = useAuthStore.getState();
  if (!state.isAuthenticated) {
    return { accessToken: null, userId: null, systemRole: null };
  }
  return {
    accessToken: state.accessToken,
    userId: state.userId,
    systemRole: SystemRole[state.systemRole],
  };
});
