import { afterEach, describe, expect, it, vi } from 'vitest';
import { computed, effectScope, ref } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import {
  useResourceDraft,
  confirmDiscard,
  discardDialog,
  resolveDiscard,
  guardResourceDrafts,
  registerDraftNavigation,
  registerDraftVisit,
} from './resource-draft';
import { selectWorkspace, workspaceId } from '../api/session';
import {
  authenticationLost,
  authenticationRestored,
  finishAuthenticationRecovery,
} from '../api/auth-loss';

const scopes: ReturnType<typeof effectScope>[] = [];
function editor(key = 'original-workspace/skill-a') {
  const identity = ref(key);
  const saved = ref<{ revision: number; value: { content: string } } | undefined>({
    revision: 1,
    value: { content: 'saved text' },
  });
  const scope = effectScope();
  scopes.push(scope);
  const state = scope.run(() => useResourceDraft(identity, saved))!;
  return { identity, saved, ...state };
}
afterEach(() => {
  resolveDiscard(false);
  scopes.splice(0).forEach((scope) => scope.stop());
  vi.unstubAllGlobals();
});

describe('resource drafts', () => {
  it('dirty drafts survive a newer saved revision', () => {
    const state = editor();
    state.draft.value!.content = 'my unsaved text';
    state.saved.value = { revision: 2, value: { content: 'other editor' } };
    expect(state.draft.value!.content).toBe('my unsaved text');
    expect(state.stale.value).toBe(true);
    expect(state.baseRevision.value).toBe(1);
  });
  it('stale saves remain disabled until explicit reload', async () => {
    const state = editor();
    state.draft.value!.content = 'my unsaved text';
    state.saved.value = { revision: 2, value: { content: 'new saved text' } };
    expect(state.canSave.value).toBe(false);
    const reload = state.reset();
    expect(discardDialog.value).toBe(true);
    resolveDiscard(false);
    expect(await reload).toBe(false);
    expect(state.draft.value!.content).toBe('my unsaved text');
    const accepted = state.reset();
    resolveDiscard(true);
    expect(await accepted).toBe(true);
    expect(state.draft.value!.content).toBe('new saved text');
    expect(state.stale.value).toBe(false);
    state.draft.value!.content = 'new edit';
    expect(state.canSave.value).toBe(true);
  });
  it('cancelled navigation retains workspace and draft', async () => {
    workspaceId.value = 'original-workspace';
    const state = editor();
    state.draft.value!.content = 'my unsaved text';
    const switching = selectWorkspace('another-workspace');
    expect(workspaceId.value).toBe('original-workspace');
    resolveDiscard(false);
    expect(await switching).toBe(false);
    expect(workspaceId.value).toBe('original-workspace');
    expect(state.draft.value!.content).toBe('my unsaved text');
    const accepted = selectWorkspace('another-workspace');
    resolveDiscard(true);
    expect(await accepted).toBe(true);
    expect(workspaceId.value).toBe('another-workspace');
  });
  it('permission loss prevents saving', () => {
    const state = editor();
    state.draft.value!.content = 'my unsaved text';
    expect(state.canSave.value).toBe(true);
    state.saved.value = undefined;
    expect(state.canSave.value).toBe(false);
    expect(state.draft.value!.content).toBe('my unsaved text');
    expect(state.adopt()({ revision: 2, value: { content: 'late success' } })).toBe(false);
  });
  it('fences late save responses across resources and preserves edits made during a save', () => {
    const state = editor();
    state.draft.value!.content = 'sent edit';
    const acceptOldSave = state.adopt();
    state.identity.value = 'original-workspace/skill-b';
    state.saved.value = { revision: 1, value: { content: 'skill b' } };
    state.draft.value!.content = 'b edit';
    expect(acceptOldSave({ revision: 2, value: { content: 'a response' } })).toBe(false);
    expect(state.draft.value!.content).toBe('b edit');
    const acceptCurrentSave = state.adopt();
    state.draft.value!.content = 'typed after save';
    expect(acceptCurrentSave({ revision: 2, value: { content: 'b edit' } })).toBe(true);
    expect(state.draft.value!.content).toBe('typed after save');
    expect(state.baseRevision.value).toBe(2);
    expect(state.dirty.value).toBe(true);
  });
  it('rejects a late save after revisiting the same resource and revision', () => {
    const state = editor();
    state.draft.value!.content = 'sent from first visit';
    const acceptFirstVisit = state.adopt();
    state.identity.value = 'original-workspace/skill-b';
    state.saved.value = { revision: 1, value: { content: 'b' } };
    state.identity.value = 'original-workspace/skill-a';
    state.saved.value = { revision: 1, value: { content: 'saved text' } };
    state.draft.value!.content = 'new visit edit';
    expect(acceptFirstVisit({ revision: 2, value: { content: 'first visit result' } })).toBe(false);
    expect(state.draft.value!.content).toBe('new visit edit');
  });
  it('advances an accepted save baseline without waiting for query refresh', () => {
    const state = editor();
    state.draft.value!.content = 'saved edit';
    const accept = state.adopt();
    expect(accept({ revision: 2, value: { content: 'saved edit' } })).toBe(true);
    expect(state.dirty.value).toBe(false);
    expect(state.stale.value).toBe(false);
    state.draft.value!.content = 'next edit';
    expect(state.canSave.value).toBe(true);
    state.saved.value = { revision: 1, value: { content: 'old cached response' } };
    expect(state.draft.value!.content).toBe('next edit');
    expect(accept({ revision: 2, value: { content: 'duplicate response' } })).toBe(false);
  });
  it('ignores a save response after its draft was explicitly discarded', async () => {
    const state = editor();
    state.draft.value!.content = 'discarded request';
    const accept = state.adopt();
    const reset = state.reset();
    resolveDiscard(true);
    await reset;
    expect(accept({ revision: 2, value: { content: 'discarded request' } })).toBe(false);
    expect(state.draft.value!.content).toBe('saved text');
  });
  it('invalidates pending save responses across permission loss and regain', () => {
    const state = editor();
    state.draft.value!.content = 'old request';
    const accept = state.adopt();
    state.saved.value = undefined;
    state.saved.value = { revision: 1, value: { content: 'saved text' } };
    expect(accept({ revision: 2, value: { content: 'old request' } })).toBe(false);
    expect(state.draft.value!.content).toBe('old request');
    expect(state.baseRevision.value).toBe(1);
  });
  it('retains the draft when a confirmed session exit fails', async () => {
    const state = editor();
    state.draft.value!.content = 'keep after logout failure';
    const exit = guardResourceDrafts(async () => false);
    resolveDiscard(true);
    expect(await exit).toBe(false);
    expect(state.draft.value!.content).toBe('keep after logout failure');
    expect(state.dirty.value).toBe(true);
  });
  it('refreshes pristine resources without mutating cached saved data', () => {
    const state = editor();
    state.saved.value = { revision: 2, value: { content: 'fresh' } };
    expect(state.draft.value!.content).toBe('fresh');
    state.draft.value!.content = 'local';
    expect(state.saved.value.value.content).toBe('fresh');
  });
  it('does not adopt the previous resource baseline while a new identity loads', () => {
    const state = editor();
    state.identity.value = 'another-workspace/skill-a';
    expect(state.draft.value).toBeUndefined();
    expect(state.canSave.value).toBe(false);
    state.saved.value = { revision: 1, value: { content: 'new workspace' } };
    expect(state.draft.value!.content).toBe('new workspace');
  });
  it('hydrates a computed saved snapshot immediately when its resource identity changes', () => {
    const identity = ref('workspace/skill-a');
    const snapshots: Record<string, { revision: number; value: { content: string } }> = {
      'workspace/skill-a': { revision: 1, value: { content: 'skill a' } },
      'workspace/skill-b': { revision: 3, value: { content: 'skill b' } },
    };
    const saved = computed(() => snapshots[identity.value]);
    const scope = effectScope();
    scopes.push(scope);
    const state = scope.run(() => useResourceDraft(identity, saved))!;
    expect(state.draft.value!.content).toBe('skill a');
    identity.value = 'workspace/skill-b';
    expect(state.draft.value!.content).toBe('skill b');
    expect(state.baseRevision.value).toBe(3);
    state.draft.value!.content = 'edit skill b';
    expect(state.canSave.value).toBe(true);
    expect(snapshots['workspace/skill-b']!.value.content).toBe('skill b');
  });
  it('discards to the acknowledged save while the query still contains an older revision', async () => {
    const state = editor();
    state.draft.value!.content = 'acknowledged revision two';
    const accept = state.adopt();
    expect(accept({ revision: 2, value: { content: 'acknowledged revision two' } })).toBe(true);
    expect(state.saved.value!.revision).toBe(1);
    state.draft.value!.content = 'discard this later edit';
    const reset = state.reset();
    resolveDiscard(true);
    expect(await reset).toBe(true);
    expect(state.draft.value!.content).toBe('acknowledged revision two');
    expect(state.baseRevision.value).toBe(2);
    expect(state.dirty.value).toBe(false);
    expect(state.stale.value).toBe(false);
  });
  it('requires confirmation for routes and shares a single pending discard decision', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: {} },
        { path: '/settings', component: {} },
      ],
    });
    await router.push('/');
    const unregister = registerDraftNavigation(router);
    const state = editor();
    state.draft.value!.content = 'my unsaved text';
    const navigation = router.push('/settings');
    await vi.waitFor(() => expect(discardDialog.value).toBe(true));
    const sameDecision = confirmDiscard();
    resolveDiscard(false);
    expect(await sameDecision).toBe(false);
    await navigation;
    expect(router.currentRoute.value.path).toBe('/');
    expect(state.dirty.value).toBe(true);
    const accepted = guardResourceDrafts();
    resolveDiscard(true);
    expect(await accepted).toBe(true);
    expect(state.dirty.value).toBe(false);
    unregister();
  });
});

describe('retained journal authentication recovery', () => {
  it('parks only the recovery routes and verified empty-workspace bootstrap, never ordinary navigation', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/editor', component: {} },
        { path: '/signin', component: {} },
        { path: '/settings', component: {} },
      ],
    });
    await router.push('/editor');
    const guard = registerDraftNavigation(router);
    let scope: 'active' | 'recovering' | 'detached' = 'recovering';
    let dirty = true;
    const flush = vi.fn(async (): Promise<void> => {
      throw new Error('not saved');
    });
    const discard = vi.fn();
    const remove = registerDraftVisit({
      identity: () => 'member-a/lab/file',
      dirty: () => dirty,
      scope: () => scope,
      flush,
      discard,
    });
    try {
      await router.push('/signin');
      expect(router.currentRoute.value.path).toBe('/editor');
      authenticationLost();
      await router.push('/settings');
      expect(router.currentRoute.value.path).toBe('/editor');
      await router.push('/signin');
      expect(router.currentRoute.value.path).toBe('/signin');
      expect(flush).not.toHaveBeenCalled();
      workspaceId.value = '';
      expect(await selectWorkspace('lab')).toBe(false);
      authenticationRestored();
      expect(await selectWorkspace('lab')).toBe(true);
      expect(await selectWorkspace('another-lab')).toBe(false);
      // A different authenticated identity can use the application without touching this draft.
      scope = 'detached';
      await router.push('/settings');
      finishAuthenticationRecovery();
      expect(router.currentRoute.value.path).toBe('/settings');
      expect(flush).not.toHaveBeenCalled();
      expect(discard).not.toHaveBeenCalled();
      expect(dirty).toBe(true);
      // Returning to its owner restores the ordinary successful-flush requirement.
      scope = 'active';
      await router.push('/editor');
      expect(router.currentRoute.value.path).toBe('/settings');
      flush.mockImplementation(async () => {
        dirty = false;
      });
      await router.push('/editor');
      expect(router.currentRoute.value.path).toBe('/editor');
      expect(discard).not.toHaveBeenCalled();
    } finally {
      remove();
      guard();
      authenticationRestored();
      finishAuthenticationRecovery();
    }
  });
});
