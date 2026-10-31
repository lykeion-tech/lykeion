import { invalidationsFor } from '../apps/dashboard/src/api/realtime';
import { keys } from '../apps/dashboard/src/api/keys';
import { describe, it, expect } from 'vitest';
import {
  acceptanceRequest,
  defaultStarters,
  restoreConversation,
} from '../apps/dashboard/src/features/coresearch/aristotle';
describe('Aristotle request and restoration state', () => {
  it('retains a UUID on transport retry, replaces it after edits or target changes', () => {
    const first = acceptanceRequest(null, 'question', '');
    expect(acceptanceRequest(first, 'question', '')).toBe(first);
    expect(acceptanceRequest(first, 'edited', '').request_id).not.toBe(first.request_id);
    expect(acceptanceRequest(first, 'question', 'welcome').request_id).not.toBe(first.request_id);
  });
  it('restores a saved conversation and falls back to the starter', () => {
    const list = [
      { id: 'welcome', is_welcome: true },
      { id: 'saved', is_welcome: false },
    ];
    expect(restoreConversation(list, 'saved')).toBe('saved');
    expect(restoreConversation(list, 'deleted')).toBe('welcome');
    expect(restoreConversation(list, 'welcome')).toBe('welcome');
    expect(restoreConversation(list.slice(0, 1), null)).toBe('welcome');
  });
  it('ships research prompts rather than workplace actions', () => {
    expect(defaultStarters).toHaveLength(3);
    expect(defaultStarters[0]!.prompt).toContain('constraints first');
  });
  it('refreshes conversation previews from actual Coresearch events', () => {
    expect(
      invalidationsFor('lab', {
        type: 'coresearch.reply',
        workspace_id: 'lab',
        card_id: null,
        payload: { project_id: 'private-project' },
      } as never),
    ).toContainEqual(keys.coresearchConversations('lab'));
  });
});
