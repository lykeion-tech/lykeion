import { computed, shallowRef } from 'vue';
import { careerStages, type Me } from './types';

type Verification = {
  epoch: number;
  stage: 'submitting' | 'checking' | 'retry';
  accepted: boolean;
};
export const verification = shallowRef<Verification>();
export const verifying = computed(() => Boolean(verification.value));
export async function checkVerifiedIdentity(state: Verification): Promise<Me | null> {
  const res = await fetch('/api/me', {
    credentials: 'same-origin',
    cache: 'no-store',
    redirect: 'error',
    signal: AbortSignal.timeout(15_000),
  });
  const user: unknown = res.ok ? await res.json() : null;
  if (state !== verification.value || state.epoch !== epoch)
    throw new DOMException('Account verification was superseded.', 'AbortError');
  if (res.status === 401) return null;
  if (!res.ok || !user || typeof user !== 'object') throw new Error('Identity unavailable');
  const me = user as Partial<Me>;
  if (
    typeof me.id !== 'string' ||
    !me.id.trim() ||
    typeof me.email !== 'string' ||
    typeof me.name !== 'string' ||
    typeof me.timezone !== 'string' ||
    typeof me.created_at !== 'string' ||
    typeof me.avatar_url !== 'string' ||
    !(me.google_sub === null || typeof me.google_sub === 'string') ||
    !(me.onboarded_at === null || typeof me.onboarded_at === 'string') ||
    !(me.career_stage === '' || careerStages.some((stage) => stage === me.career_stage))
  )
    throw new Error('Invalid identity response');
  return me as Me;
}
let epoch = 0;
let dataGeneration = 0;
export function invalidateAuthenticationData() {
  dataGeneration++;
}
let recovery: 'normal' | 'lost' | 'restoring' = 'normal';
export const authenticationIsLost = () => recovery === 'lost';
export const authenticationIsRestoring = () => recovery === 'restoring';
export function finishAuthenticationRecovery() {
  if (recovery === 'restoring') recovery = 'normal';
}
export const authenticationEpoch = () => epoch;
export const authenticationDataGeneration = () => dataGeneration;
export function advanceAuthenticationEpoch() {
  epoch++;
  invalidateAuthenticationData();
  if (verification.value) verification.value = { epoch, stage: 'retry', accepted: false };
}

const listeners = new Set<() => void>();

export function authenticationLost(requestEpoch = epoch) {
  if (requestEpoch !== epoch) return;
  if (recovery !== 'lost') invalidateAuthenticationData();
  recovery = 'lost';
  for (const listener of listeners) listener();
}

export function onAuthenticationLost(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

const restoredListeners = new Set<() => void>();

export function authenticationRestored(verifiedEpoch?: number) {
  if (verifiedEpoch !== epoch) advanceAuthenticationEpoch();
  else invalidateAuthenticationData();
  // Verification already advanced the epoch before dispatch.
  recovery = 'restoring';
  for (const listener of restoredListeners) listener();
}

export function onAuthenticationRestored(listener: () => void) {
  restoredListeners.add(listener);
  return () => restoredListeners.delete(listener);
}

const credentialListeners = new Set<() => void>();
export function onCredentialsCommitted(listener: () => void) {
  credentialListeners.add(listener);
  return () => credentialListeners.delete(listener);
}
export function credentialsCommitted() {
  for (const listener of credentialListeners) listener();
}
