let epoch = 0;
let recovery: 'normal' | 'lost' | 'restoring' = 'normal';
export const authenticationIsLost = () => recovery === 'lost';
export const authenticationIsRestoring = () => recovery === 'restoring';
export function finishAuthenticationRecovery() {
  if (recovery === 'restoring') recovery = 'normal';
}
export const authenticationEpoch = () => epoch;

const listeners = new Set<() => void>();

export function authenticationLost(requestEpoch = epoch) {
  if (requestEpoch !== epoch) return;
  recovery = 'lost';
  for (const listener of listeners) listener();
}

export function onAuthenticationLost(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

const restoredListeners = new Set<() => void>();

export function authenticationRestored() {
  epoch++;
  // Only successful SignIn verification calls this in the application.
  recovery = 'restoring';
  for (const listener of restoredListeners) listener();
}

export function onAuthenticationRestored(listener: () => void) {
  restoredListeners.add(listener);
  return () => restoredListeners.delete(listener);
}
