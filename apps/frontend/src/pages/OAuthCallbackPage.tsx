import { useEffect, useState } from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { ConnectError } from '@connectrpc/connect';
import { OAUTH_SESSION_KEY, useAuthStore } from '../stores/auth';

function toErrorMessage(err: unknown, fallback: string): string {
  if (err instanceof ConnectError) return err.rawMessage || fallback;
  if (err instanceof Error) return err.message || fallback;
  return fallback;
}

/**
 * Landing page for the OAuth redirect: reads ?code & ?state, verifies the state
 * against what BeginOAuth stashed in sessionStorage, and exchanges the code for
 * a session. Provider preference: router location.state, then sessionStorage,
 * then a default. The stashed payload may carry a returnTo destination so the
 * user lands back where they started (e.g. /profile instead of /rooms).
 */
export default function OAuthCallbackPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const oauthComplete = useAuthStore((s) => s.oauthComplete);

  const [pending, setPending] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const code = params.get('code');
    const stateParam = params.get('state') ?? '';

    const routerState = location.state as { provider?: string; returnTo?: string } | null;
    let provider = routerState?.provider ?? '';
    let returnTo = routerState?.returnTo ?? '';
    let expectedState: string | null = null;
    try {
      const raw = sessionStorage.getItem(OAUTH_SESSION_KEY);
      if (raw) {
        const parsed = JSON.parse(raw) as { provider?: string; state?: string; returnTo?: string };
        if (!provider) provider = parsed.provider ?? '';
        if (!returnTo) returnTo = parsed.returnTo ?? '';
        expectedState = parsed.state ?? null;
      }
    } catch {
      // Malformed storage — fall back to defaults below.
    }
    if (!provider) provider = 'spotify';

    if (!code) {
      setPending(false);
      setError('The provider did not return an authorization code.');
      return;
    }
    if (expectedState && stateParam && expectedState !== stateParam) {
      setPending(false);
      setError('OAuth state mismatch — please try signing in again.');
      return;
    }

    let cancelled = false;
    oauthComplete(provider, code, stateParam)
      .then(() => {
        if (cancelled) return;
        sessionStorage.removeItem(OAUTH_SESSION_KEY);
        // returnTo is only honored for known destinations; anything else (or
        // nothing at all) falls back to the room browser.
        const destination = returnTo === '/profile' ? '/profile' : '/rooms';
        navigate(destination, { replace: true });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setPending(false);
        setError(toErrorMessage(err, 'Failed to complete sign-in.'));
      });

    return () => {
      cancelled = true;
    };
  }, [oauthComplete, navigate, location.state]);

  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      <div className="card w-full max-w-sm text-center">
        {pending ? (
          <>
            <div className="mx-auto mb-4 h-2 w-2 animate-pulse rounded-full bg-yellow-400" />
            <p className="text-sm text-gray-300">Completing sign-in…</p>
            <p className="mt-1 text-xs text-gray-500">Exchanging your authorization code.</p>
          </>
        ) : (
          <>
            <div className="mx-auto mb-4 h-2 w-2 rounded-full bg-red-500" />
            <p className="text-sm font-medium text-red-300">Sign-in failed</p>
            <p className="mt-1 text-xs text-gray-400">{error}</p>
            <Link to="/login" className="btn btn-primary mt-5 w-full">
              Back to login
            </Link>
          </>
        )}
      </div>
    </div>
  );
}
