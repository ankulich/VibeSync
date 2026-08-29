import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { ConnectError } from '@connectrpc/connect';
import { getAuthClient, getUserClient } from '../api/clients';
import { OAUTH_SESSION_KEY, useAuthStore } from '../stores/auth';
import { SystemRole } from '../gen/vibesync/common/v1/common_pb';
import type { LinkedProvider } from '../gen/vibesync/auth/v1/auth_pb';

function toErrorMessage(err: unknown, fallback: string): string {
  if (err instanceof ConnectError) return err.rawMessage || fallback;
  if (err instanceof Error) return err.message || fallback;
  return fallback;
}

const ROLE_LABEL: Record<SystemRole, string> = {
  [SystemRole.UNSPECIFIED]: 'Unknown',
  [SystemRole.GUEST]: 'Guest',
  [SystemRole.USER]: 'User',
  [SystemRole.MODERATOR]: 'Moderator',
  [SystemRole.ADMINISTRATOR]: 'Administrator',
};

interface ProviderConfig {
  /** Wire name understood by BeginOAuth / ListLinkedProviders. */
  name: string;
  label: string;
  icon: string;
  /** Tailwind classes tinting the card icon/badge with the provider's brand color. */
  iconClass: string;
  badgeClass: string;
  buttonClass: string;
}

const PROVIDERS: ProviderConfig[] = [
  {
    name: 'spotify',
    label: 'Spotify',
    icon: '♫',
    iconClass: 'bg-emerald-500/15 text-emerald-400',
    badgeClass: 'bg-emerald-500/15 text-emerald-300',
    buttonClass: 'btn btn-primary',
  },
  {
    name: 'google',
    label: 'YouTube',
    icon: '▶',
    iconClass: 'bg-red-500/15 text-red-400',
    badgeClass: 'bg-red-500/15 text-red-300',
    buttonClass: 'btn btn-primary',
  },
];

function formatLinkedAt(linked: LinkedProvider | undefined): string | null {
  if (!linked?.linkedAt) return null;
  const date = linked.linkedAt.toDate();
  return Number.isNaN(date.getTime()) ? null : date.toLocaleDateString();
}

export default function ProfilePage() {
  const navigate = useNavigate();
  const logout = useAuthStore((s) => s.logout);
  const userId = useAuthStore((s) => s.userId);
  const systemRole = useAuthStore((s) => s.systemRole);

  const [connecting, setConnecting] = useState<string | null>(null);
  const [connectError, setConnectError] = useState<string | null>(null);

  const userQuery = useQuery({
    queryKey: ['user', userId],
    queryFn: () => getUserClient().getUser({ id: { value: userId ?? '' } }),
    enabled: !!userId,
  });

  const providersQuery = useQuery({
    queryKey: ['linked-providers', userId],
    queryFn: () => getAuthClient().listLinkedProviders({ userId: { value: userId ?? '' } }),
    enabled: !!userId,
  });

  const linked = new Map<string, LinkedProvider>();
  for (const provider of providersQuery.data?.providers ?? []) {
    linked.set(provider.provider, provider);
  }

  const startConnect = async (provider: string) => {
    setConnectError(null);
    setConnecting(provider);
    try {
      const response = await getAuthClient().beginOAuth({
        provider,
        redirectUri: `${window.location.origin}/oauth/callback`,
      });
      if (!response.authorizationUrl) {
        throw new Error('No authorization URL returned');
      }
      // Remember provider + state + where to land afterwards so the callback
      // page can verify the round trip and send the user back here.
      sessionStorage.setItem(
        OAUTH_SESSION_KEY,
        JSON.stringify({ provider, state: response.state, returnTo: '/profile' }),
      );
      window.location.href = response.authorizationUrl;
    } catch (err) {
      setConnectError(toErrorMessage(err, `Could not start ${provider} connection.`));
      setConnecting(null);
    }
  };

  const user = userQuery.data?.user;

  return (
    <div className="mx-auto min-h-screen w-full max-w-3xl p-6">
      <header className="mb-8 flex items-center justify-between gap-4">
        <h1 className="text-2xl font-bold tracking-tight">Profile</h1>
        <div className="flex items-center gap-2">
          <Link to="/rooms" className="btn btn-ghost">
            ← Rooms
          </Link>
          <button
            type="button"
            className="btn btn-ghost"
            onClick={() => {
              void logout().finally(() => navigate('/login'));
            }}
          >
            Sign out
          </button>
        </div>
      </header>

      <section className="card mb-6">
        <h2 className="mb-4 text-sm font-semibold uppercase tracking-wide text-gray-400">
          Account
        </h2>
        {userQuery.isLoading ? (
          <p className="text-sm text-gray-400">Loading account details…</p>
        ) : userQuery.isError ? (
          <p className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-300">
            Could not load account details. {toErrorMessage(userQuery.error, '')}
          </p>
        ) : (
          <dl className="grid gap-4 sm:grid-cols-3">
            <div>
              <dt className="text-xs font-medium text-gray-400">Email</dt>
              <dd className="mt-1 truncate text-sm">{user?.email || '—'}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-gray-400">Username</dt>
              <dd className="mt-1 truncate text-sm">{user?.username || '—'}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium text-gray-400">Role</dt>
              <dd className="mt-1 text-sm">{ROLE_LABEL[systemRole] ?? 'Unknown'}</dd>
            </div>
          </dl>
        )}
      </section>

      <section className="card">
        <h2 className="mb-1 text-sm font-semibold uppercase tracking-wide text-gray-400">
          Streaming Services
        </h2>
        <p className="mb-4 text-xs text-gray-500">
          Connect a provider to search and queue music on its behalf.
        </p>

        {providersQuery.isLoading && <p className="text-sm text-gray-400">Loading services…</p>}

        {providersQuery.isError && (
          <p className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-300">
            Could not load streaming services.{' '}
            {toErrorMessage(providersQuery.error, 'Please try again later.')}
          </p>
        )}

        {connectError && (
          <p className="mb-4 rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-300">
            {connectError}
          </p>
        )}

        {!providersQuery.isLoading && !providersQuery.isError && (
          <div className="grid gap-4 sm:grid-cols-2">
            {PROVIDERS.map((provider) => {
              const link = linked.get(provider.name);
              const linkedAt = formatLinkedAt(link);
              return (
                <div key={provider.name} className="card flex items-center gap-4">
                  <span
                    aria-hidden="true"
                    className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-lg font-bold ${provider.iconClass}`}
                  >
                    {provider.icon}
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-semibold">{provider.label}</p>
                    {link ? (
                      <p className="mt-0.5 text-xs text-emerald-300">
                        Connected{linkedAt ? ` · ${linkedAt}` : ''}
                      </p>
                    ) : (
                      <p className="mt-0.5 text-xs text-gray-500">Not connected</p>
                    )}
                  </div>
                  {link ? (
                    <span
                      className={`shrink-0 rounded-full px-2.5 py-1 text-xs font-medium ${provider.badgeClass}`}
                    >
                      Connected ✓
                    </span>
                  ) : (
                    <button
                      type="button"
                      className={`${provider.buttonClass} shrink-0 px-3 py-1.5 text-xs`}
                      disabled={connecting != null}
                      onClick={() => void startConnect(provider.name)}
                    >
                      {connecting === provider.name ? 'Redirecting…' : 'Connect'}
                    </button>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </section>
    </div>
  );
}
