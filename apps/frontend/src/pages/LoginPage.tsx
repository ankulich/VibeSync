import { useState, type FormEvent } from 'react';
import { Link, Navigate, useNavigate } from 'react-router-dom';
import { ConnectError } from '@connectrpc/connect';
import { getAuthClient } from '../api/clients';
import { OAUTH_SESSION_KEY, useAuthStore } from '../stores/auth';

function toErrorMessage(err: unknown, fallback: string): string {
  if (err instanceof ConnectError) return err.rawMessage || fallback;
  if (err instanceof Error) return err.message || fallback;
  return fallback;
}

export default function LoginPage() {
  const navigate = useNavigate();
  const login = useAuthStore((s) => s.login);
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);

  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [oauthBusy, setOauthBusy] = useState<string | null>(null);

  if (isAuthenticated) {
    return <Navigate to="/rooms" replace />;
  }

  const onSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await login(email.trim(), password);
      navigate('/rooms', { replace: true });
    } catch (err) {
      setError(toErrorMessage(err, 'Login failed. Check your credentials.'));
    } finally {
      setSubmitting(false);
    }
  };

  const startOAuth = async (provider: string) => {
    setError(null);
    setOauthBusy(provider);
    try {
      const response = await getAuthClient().beginOAuth({
        provider,
        redirectUri: `${window.location.origin}/oauth/callback`,
      });
      if (!response.authorizationUrl) {
        throw new Error('No authorization URL returned');
      }
      // Remember provider + state so the callback page can verify the round trip.
      sessionStorage.setItem(
        OAUTH_SESSION_KEY,
        JSON.stringify({ provider, state: response.state }),
      );
      window.location.href = response.authorizationUrl;
    } catch (err) {
      setError(toErrorMessage(err, `Could not start ${provider} sign-in.`));
      setOauthBusy(null);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <h1 className="text-3xl font-bold tracking-tight">
            Vibe<span className="text-accent">Sync</span>
          </h1>
          <p className="mt-2 text-sm text-gray-400">Listen together, perfectly in sync.</p>
        </div>

        <div className="card space-y-4">
          <form onSubmit={onSubmit} className="space-y-3">
            <div>
              <label htmlFor="email" className="mb-1 block text-xs font-medium text-gray-400">
                Email
              </label>
              <input
                id="email"
                type="email"
                className="input"
                placeholder="you@example.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                autoComplete="email"
                required
              />
            </div>
            <div>
              <label htmlFor="password" className="mb-1 block text-xs font-medium text-gray-400">
                Password
              </label>
              <input
                id="password"
                type="password"
                className="input"
                placeholder="••••••••"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                required
              />
            </div>
            <button type="submit" className="btn btn-primary w-full" disabled={submitting}>
              {submitting ? 'Signing in…' : 'Sign in'}
            </button>
          </form>

          <div className="flex items-center gap-3 text-xs text-gray-500">
            <span className="h-px flex-1 bg-gray-700" />
            or continue with
            <span className="h-px flex-1 bg-gray-700" />
          </div>

          <div className="grid grid-cols-2 gap-2">
            <button
              type="button"
              className="btn btn-ghost"
              disabled={oauthBusy != null}
              onClick={() => void startOAuth('spotify')}
            >
              {oauthBusy === 'spotify' ? 'Redirecting…' : 'Spotify'}
            </button>
            <button
              type="button"
              className="btn btn-ghost"
              disabled={oauthBusy != null}
              onClick={() => void startOAuth('google')}
            >
              {oauthBusy === 'google' ? 'Redirecting…' : 'Google'}
            </button>
          </div>

          {error && (
            <p className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-300">
              {error}
            </p>
          )}
        </div>

        <p className="mt-6 text-center text-xs text-gray-500">
          Already signed in?{' '}
          <Link to="/rooms" className="text-accent hover:underline">
            Go to rooms
          </Link>
        </p>

        <p className="mt-2 text-center text-xs text-gray-500">
          Don't have an account?{' '}
          <Link to="/register" className="text-accent hover:underline">
            Create one
          </Link>
        </p>
      </div>
    </div>
  );
}
