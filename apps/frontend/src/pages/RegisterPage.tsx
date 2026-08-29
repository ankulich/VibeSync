import { useState, type FormEvent } from 'react';
import { Link, Navigate, useNavigate } from 'react-router-dom';
import { ConnectError } from '@connectrpc/connect';
import { getAuthClient } from '../api/clients';
import { OAUTH_SESSION_KEY, useAuthStore } from '../stores/auth';

function toErrorMessage(err: unknown, fallback: string): string {
  if (err instanceof ConnectError) {
    // err.message carries the code prefix ("ALREADY_EXISTS: ..."), rawMessage does not.
    if (err.message.includes('ALREADY_EXISTS')) return 'Email already registered';
    return err.rawMessage || fallback;
  }
  if (err instanceof Error) return err.message || fallback;
  return fallback;
}

export default function RegisterPage() {
  const navigate = useNavigate();
  const register = useAuthStore((s) => s.register);
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);

  const [email, setEmail] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [oauthBusy, setOauthBusy] = useState<string | null>(null);

  if (isAuthenticated) {
    return <Navigate to="/rooms" replace />;
  }

  const onSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(null);

    const trimmedEmail = email.trim();
    const trimmedUsername = username.trim();
    if (!trimmedEmail || !trimmedUsername || !password || !confirmPassword) {
      setError('Please fill in all fields.');
      return;
    }
    if (password.length < 8) {
      setError('Password must be at least 8 characters.');
      return;
    }
    if (password !== confirmPassword) {
      setError('Passwords do not match.');
      return;
    }

    setSubmitting(true);
    try {
      await register(trimmedEmail, trimmedUsername, password);
      navigate('/rooms', { replace: true });
    } catch (err) {
      setError(toErrorMessage(err, 'Registration failed. Please try again.'));
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
          <p className="mt-2 text-sm text-gray-400">Create your account and listen in sync.</p>
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
              <label htmlFor="username" className="mb-1 block text-xs font-medium text-gray-400">
                Username
              </label>
              <input
                id="username"
                type="text"
                className="input"
                placeholder="3-32 chars, letters/digits/_"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                title="3-32 characters: letters, digits and underscores; must start with a letter"
                pattern="[A-Za-z][A-Za-z0-9_]{2,31}"
                maxLength={32}
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
                placeholder="At least 8 characters"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
                minLength={8}
                required
              />
            </div>
            <div>
              <label htmlFor="confirm-password" className="mb-1 block text-xs font-medium text-gray-400">
                Confirm password
              </label>
              <input
                id="confirm-password"
                type="password"
                className="input"
                placeholder="••••••••"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                autoComplete="new-password"
                required
              />
            </div>
            <button type="submit" className="btn btn-primary w-full" disabled={submitting}>
              {submitting ? 'Creating account…' : 'Create account'}
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
          Already have an account?{' '}
          <Link to="/login" className="text-accent hover:underline">
            Sign in
          </Link>
        </p>
      </div>
    </div>
  );
}
