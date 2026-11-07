import type { InjectionKey } from 'vue';
import { onAuthenticationLost, onAuthenticationRestored } from '../api/auth-loss';
import type { DesktopDaemonBridge } from '../../../desktop/src/daemon-types';

export const daemonSessionKey: InjectionKey<ReturnType<typeof createDaemonSession>> =
  Symbol('daemon-session');

const stopFailure =
  'The desktop daemon could not be stopped. Retry logging out before changing accounts.';
const configureFailure =
  'The desktop daemon could not be configured. Check Daemon in Settings → Machines.';

export function createDaemonSession(options: {
  bridge?: DesktopDaemonBridge;
  apiOrigin: string;
  token: () => Promise<{ token: string }>;
  reportError: (message: string) => void;
  onConfigured?: () => void;
}) {
  let identity: string | null = null;
  let generation = 0;
  let disposed = false;
  let configured = false;
  let pending = false;
  let authenticationBlocked = false;
  let clearing: Promise<void> = Promise.resolve();
  const { bridge } = options;

  function clearSession(blockAuthentication: boolean): Promise<void> {
    if (blockAuthentication) authenticationBlocked = true;
    generation++;
    identity = null;
    configured = false;
    pending = false;
    if (!bridge || disposed) return Promise.resolve();
    clearing = bridge.clearSession().then(
      () => {},
      () => {
        options.reportError(stopFailure);
        throw new Error(stopFailure);
      },
    );
    // Keep a failed clear as a barrier for subsequent account configuration.
    void clearing.catch(() => {});
    return clearing;
  }

  const clear = () => clearSession(true);

  async function update(userID: string | undefined, unauthorized = false): Promise<void> {
    if (!bridge || disposed) return;
    if (unauthorized) {
      if (identity !== null || generation === 0) await clear().catch(() => {});
      return;
    }
    // No user during initial loading or a transient network failure is not a logout.
    if (authenticationBlocked || !userID || (userID === identity && (configured || pending)))
      return;
    if (identity !== null && userID !== identity) void clearSession(false).catch(() => {});
    identity = userID;
    pending = true;
    const visit = ++generation;
    const current = () => !disposed && visit === generation && identity === userID;
    try {
      await clearing;
      if (!current()) return;
      if ((await bridge.getSnapshot()).availability !== 'available' || !current()) return;
      const { token } = await options.token();
      if (!current()) return;
      await bridge.configureSession({ apiOrigin: options.apiOrigin, userID, sessionToken: token });
      if (current()) {
        configured = true;
        options.onConfigured?.();
      }
    } catch {
      if (current()) options.reportError(configureFailure);
    } finally {
      if (current()) pending = false;
    }
  }
  const unsubscribe = onAuthenticationLost(() => {
    void update(undefined, true);
  });
  const unsubscribeRestored = onAuthenticationRestored(() => {
    authenticationBlocked = false;
  });
  return {
    update,
    clear,
    dispose: () => {
      unsubscribe();
      unsubscribeRestored();
      disposed = true;
      generation++;
    },
  };
}
