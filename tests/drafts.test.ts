import { afterEach, describe, expect, it, vi } from 'vitest';
import { effectScope, ref } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import {
  useResourceDraft,
  confirmDiscard,
  discardDialog,
  resolveDiscard,
  guardResourceDrafts,
  registerDraftNavigation,
} from '../apps/dashboard/src/state/resource-draft';
import { selectWorkspace, workspaceId } from '../apps/dashboard/src/api/session';

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
