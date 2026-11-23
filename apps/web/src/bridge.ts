// The dashboard is served beside the landing page under /app/ on the same
// origin, so the session cookie set by sign-in is sent with the landing page's
// requests too.
export const paths = {
  login: '/app/#/signin',
  onboarding: '/app/#/onboarding',
  newWorkspace: '/app/#/onboarding?step=workspace',
  coresearch: '/app/#/coresearch',
};

// While the workspace list is in flight the button points at the dashboard
// root, which resolves the destination itself. That keeps the button working
// without hiding or disabling it.
const LOADING_FALLBACK_HREF = '/app/';

// Hosts that serve the public marketing site. There `/` stays public even for
// signed-in visitors; on any other host (local, self-hosted) they go straight
// to the dashboard.
const OFFICIAL_MARKETING_HOSTS = new Set<string>();

export interface Me {
  onboarded_at?: string | null;
}

export function resolvePostAuthDestination(
  workspaces: readonly unknown[],
  hasOnboarded: boolean,
): string {
  if (!hasOnboarded) return paths.onboarding;
  return workspaces.length ? paths.coresearch : paths.newWorkspace;
}

export function resolveDashboardCtaHref(state: {
  isAuthenticated: boolean;
  workspaceListReady: boolean;
  workspaces: readonly unknown[];
  hasOnboarded: boolean;
}): string {
  if (!state.isAuthenticated) return paths.login;
  if (!state.workspaceListReady) return LOADING_FALLBACK_HREF;
  return resolvePostAuthDestination(state.workspaces, state.hasOnboarded);
}

export function isOfficialMarketingHost(hostname: string): boolean {
  return OFFICIAL_MARKETING_HOSTS.has(hostname.trim().toLowerCase().replace(/\.$/, ''));
}

async function read<T>(path: string): Promise<T | undefined> {
  try {
    const response = await fetch(path, { credentials: 'same-origin' });
    return response.ok ? ((await response.json()) as T) : undefined;
  } catch {
    return undefined;
  }
}

export async function loadSession(): Promise<{ user: Me | null; destination: string | null }> {
  const user = await read<Me>('/api/me');
  if (!user) return { user: null, destination: null };
  const workspaces = await read<unknown[] | null>('/api/workspaces');
  if (workspaces === undefined) return { user, destination: null };
  return { user, destination: resolvePostAuthDestination(workspaces ?? [], !!user.onboarded_at) };
}
