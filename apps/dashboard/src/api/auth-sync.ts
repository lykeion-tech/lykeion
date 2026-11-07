const listeners = new Set<() => void>();
let channel: BroadcastChannel | undefined;
let storageListener: ((event: StorageEvent) => void) | undefined;
const storageKey = 'lykeion.auth-changed';
const googlePendingKey = 'lykeion.google-auth-pending';

function connect() {
  if (typeof window === 'undefined' || channel || storageListener) return;
  if (typeof window.BroadcastChannel === 'function') {
    try {
      channel = new window.BroadcastChannel('lykeion.auth-changed');
      channel.onmessage = (event) => {
        if (event.data === 'changed') for (const listener of listeners) listener();
      };
      return;
    } catch {
      // Some browsers disable cross-window channels in private sessions.
    }
  }
  if (typeof window.addEventListener === 'function') {
    storageListener = (event) => {
      if (event.key === storageKey && event.newValue) for (const listener of listeners) listener();
    };
    window.addEventListener('storage', storageListener);
  }
}

export function onExternalAuthenticationChanged(listener: () => void) {
  listeners.add(listener);
  connect();
  return () => {
    listeners.delete(listener);
    if (listeners.size) return;
    channel?.close();
    channel = undefined;
    if (storageListener) window.removeEventListener('storage', storageListener);
    storageListener = undefined;
  };
}

export function announceAuthenticationChanged() {
  connect();
  if (channel) channel.postMessage('changed');
  else if (typeof window !== 'undefined') {
    try {
      window.localStorage.setItem(storageKey, `${Date.now()}-${Math.random()}`);
    } catch {
      // Storage may be unavailable in a private browsing session.
    }
  }
}

export function markGoogleAuthenticationPending() {
  try {
    window.sessionStorage.setItem(googlePendingKey, '1');
  } catch {
    // The redirect still works when session storage is unavailable.
  }
}

export function announceReturnedGoogleAuthentication() {
  try {
    if (window.sessionStorage.getItem(googlePendingKey) !== '1') return;
    window.sessionStorage.removeItem(googlePendingKey);
    announceAuthenticationChanged();
  } catch {
    // The redirect still works when session storage is unavailable.
  }
}
