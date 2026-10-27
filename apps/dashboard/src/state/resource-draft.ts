import { computed, getCurrentScope, onScopeDispose, readonly, ref, watch, type Ref } from 'vue';
import type { Router } from 'vue-router';

type Saved<T> = { revision: number; value: T };
type DraftVisit = { dirty: () => boolean; discard: () => void; identity: () => string };
const visits = new Set<DraftVisit>();

// Editors with their own save semantics share the same guarded exit decision.
export function registerDraftVisit(visit: DraftVisit) {
  visits.add(visit);
  const unregister = () => {
    visits.delete(visit);
  };
  if (getCurrentScope()) onScopeDispose(unregister);
  return unregister;
}

const showingDiscard = ref(false);
export const discardDialog = readonly(showingDiscard);
let pendingDiscard: Promise<boolean> | undefined;
let settleDiscard: ((discard: boolean) => void) | undefined;

// All exits share one explicit decision, including nested modal Escape events.
export function confirmDiscard(): Promise<boolean> {
  if (pendingDiscard) return pendingDiscard;
  showingDiscard.value = true;
  pendingDiscard = new Promise<boolean>((resolve) => {
    settleDiscard = resolve;
  });
  return pendingDiscard;
}

export function resolveDiscard(discard: boolean) {
  const resolve = settleDiscard;
  settleDiscard = undefined;
  pendingDiscard = undefined;
  showingDiscard.value = false;
  resolve?.(discard);
}

export async function guardResourceDrafts(proceed?: () => Promise<boolean>): Promise<boolean> {
  const dirty = [...visits]
    .filter((visit) => visit.dirty())
    .map((visit) => ({
      visit,
      identity: visit.identity(),
    }));
  if (dirty.length && !(await confirmDiscard())) return false;
  if (proceed && !(await proceed())) return false;
  for (const { visit, identity } of dirty) {
    if (visits.has(visit) && visit.identity() === identity) visit.discard();
  }
  return true;
}

export function registerDraftNavigation(router: Pick<Router, 'beforeEach'>) {
  return router.beforeEach((to, from) => to.fullPath === from.fullPath || guardResourceDrafts());
}

// Resource payloads are JSON values. Copy them so typing never mutates the query cache.
function copy<T>(value: T): T {
  if (Array.isArray(value)) return value.map(copy) as T;
  if (value && typeof value === 'object')
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, copy(item)])) as T;
  return value;
}
function equal(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (!a || !b || typeof a !== 'object' || typeof b !== 'object') return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  const keys = Object.keys(a);
  return (
    keys.length === Object.keys(b).length &&
    keys.every(
      (key) =>
        Object.hasOwn(b, key) &&
        equal((a as Record<string, unknown>)[key], (b as Record<string, unknown>)[key]),
    )
  );
}

export function useResourceDraft<T>(identity: Ref<string>, saved: Ref<Saved<T> | undefined>) {
  const draft = ref<T>() as Ref<T | undefined>;
  const baseline = ref<T>() as Ref<T | undefined>;
  const baseRevision = ref<number>();
  let ignoredSaved: Saved<T> | undefined;
  let generation = 0;
  const dirty = computed(() => !equal(draft.value, baseline.value));
  const available = computed(() =>
    Boolean(identity.value && saved.value && saved.value !== ignoredSaved),
  );
  const stale = computed(
    () =>
      available.value &&
      baseRevision.value !== undefined &&
      saved.value!.revision > baseRevision.value,
  );
  const canSave = computed(
    () => available.value && baseRevision.value !== undefined && dirty.value && !stale.value,
  );

  function replace(snapshot: Saved<T> | undefined) {
    baseline.value = snapshot ? copy(snapshot.value) : undefined;
    draft.value = snapshot ? copy(snapshot.value) : undefined;
    baseRevision.value = snapshot?.revision;
  }
  function discard() {
    generation++;
    if (
      available.value &&
      baseRevision.value !== undefined &&
      saved.value!.revision < baseRevision.value
    ) {
      // An acknowledged save can precede the query cache refresh.
      draft.value = copy(baseline.value);
      return;
    }
    replace(available.value ? saved.value : undefined);
  }
  watch(
    [identity, saved],
    ([key, snapshot], [previousKey, previousSnapshot]) => {
      if (key !== previousKey) {
        generation++;
        // A matching computed snapshot can change in the same transition.
        // Only retain the fence when the query still exposes its previous value.
        ignoredSaved = snapshot === previousSnapshot ? snapshot : undefined;
        replace(undefined);
      }
      if (!snapshot) {
        generation++;
        return;
      }
      if (!key || snapshot === ignoredSaved) return;
      ignoredSaved = undefined;
      if (
        !dirty.value &&
        (baseRevision.value === undefined || snapshot.revision >= baseRevision.value)
      )
        replace(snapshot);
    },
    { immediate: true, flush: 'sync' },
  );

  async function reset() {
    const key = identity.value;
    if (dirty.value && !(await confirmDiscard())) return false;
    if (identity.value !== key) return false;
    discard();
    return true;
  }
  // Call before awaiting a save, then pass its response to the returned callback.
  // Capturing the visit also fences A-to-B-to-A navigation and later typing.
  function adopt() {
    const token = {
      identity: identity.value,
      generation,
      revision: baseRevision.value,
      value: copy(draft.value),
      allowed: canSave.value,
    };
    return (snapshot: Saved<T>): boolean => {
      if (
        !token.allowed ||
        !available.value ||
        identity.value !== token.identity ||
        generation !== token.generation ||
        baseRevision.value !== token.revision ||
        snapshot.revision <= token.revision! ||
        snapshot.revision < saved.value!.revision
      )
        return false;
      const unchanged = equal(draft.value, token.value);
      baseline.value = copy(snapshot.value);
      baseRevision.value = snapshot.revision;
      if (unchanged) draft.value = copy(snapshot.value);
      return true;
    };
  }
  const visit: DraftVisit = { dirty: () => dirty.value, discard, identity: () => identity.value };
  registerDraftVisit(visit);
  if (getCurrentScope())
    onScopeDispose(() => {
      generation++;
    });
  return { draft, dirty, stale, baseRevision, reset, adopt, canSave };
}
