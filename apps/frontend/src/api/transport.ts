import { createConnectTransport } from '@connectrpc/connect-web';
import type { Interceptor } from '@connectrpc/connect';

/**
 * Auth context attached to every outgoing RPC as headers. Supplied by the auth
 * store via {@link setAuthContextGetter} so this module never imports the store
 * directly (keeps the module graph acyclic: stores/auth -> api/clients -> api/transport).
 */
export interface AuthContext {
  accessToken: string | null;
  userId: string | null;
  systemRole: string | null;
}

type AuthContextGetter = () => AuthContext;

const unauthenticated: AuthContext = { accessToken: null, userId: null, systemRole: null };

let authContextGetter: AuthContextGetter = () => unauthenticated;

/** Registers the function used to resolve auth headers at request time. */
export function setAuthContextGetter(getter: AuthContextGetter): void {
  authContextGetter = getter;
}

/**
 * Connect interceptor that attaches VibeSync auth headers to every request:
 *   Authorization: Bearer <access token>
 *   X-Vibesync-User-Id: <user id>
 *   X-Vibesync-System-Role: <role name>
 */
const authInterceptor: Interceptor = (next) => (req) => {
  const { accessToken, userId, systemRole } = authContextGetter();
  if (accessToken) {
    req.header.set('Authorization', `Bearer ${accessToken}`);
  }
  if (userId) {
    req.header.set('X-Vibesync-User-Id', userId);
  }
  if (systemRole) {
    req.header.set('X-Vibesync-System-Role', systemRole);
  }
  return next(req);
};

/**
 * Creates a Connect transport that issues requests relative to the current
 * origin (baseUrl: ""). In dev the Vite proxy routes each /vibesync.<service>
 * prefix to the matching backend, so no CORS or absolute URLs are needed.
 */
export function createTransport() {
  return createConnectTransport({
    baseUrl: '',
    interceptors: [authInterceptor],
  });
}

/** Shared transport used by the client singletons in clients.ts. */
export const transport = createTransport();
