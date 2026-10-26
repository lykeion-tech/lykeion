let epoch = 0;
export const authenticationEpoch = () => epoch;

const listeners = new Set<() => void>();

export function authenticationLost(requestEpoch = epoch) {
  if (requestEpoch !== epoch) return;
  for (const listener of listeners) listener();
}

export function onAuthenticationLost(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

const restoredListeners = new Set<() => void>();

export function authenticationRestored() {
  epoch++;
  for (const listener of restoredListeners) listener();
}

export function onAuthenticationRestored(listener: () => void) {
  restoredListeners.add(listener);
  return () => restoredListeners.delete(listener);
}
