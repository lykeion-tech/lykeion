import { describe, expect, it } from 'vitest';
import {
  blankManualDecisionDraft,
  manualDecisionDraftError,
  mergeManualDecisionDraft,
  parseManualDecisionProposal,
  manualDeadlineLabel,
} from './manualDecisionDraft';
const base = () => ({ ...blankManualDecisionDraft(), title: 'Original' });
describe('manual draft validation', () => {
  it('enforces one-line unicode title and optional context boundaries', () => {
    for (const title of ['a', '🧪'.repeat(200)])
      expect(manualDecisionDraftError({ ...base(), title })).toBe('');
    for (const title of ['', ' ', 'a\nb', 'a\rb', 'a'.repeat(201)])
      expect(manualDecisionDraftError({ ...base(), title })).not.toBe('');
    expect(manualDecisionDraftError({ ...base(), context: 'a'.repeat(4000) })).toBe('');
    expect(manualDecisionDraftError({ ...base(), context: 'a'.repeat(4001) })).not.toBe('');
    expect(manualDecisionDraftError({ ...base(), deadline: '2099-12-31' })).toBe('');
    expect(manualDecisionDraftError({ ...base(), deadline: '2027-02-29' })).not.toBe('');
  });
  it('rejects unknown, partial and invalid proposals without updates', () => {
    for (const draft of [
      { title: 'New' },
      { ...base(), secret: 'x' },
      { ...base(), deadline: 'tomorrow' },
    ]) {
      expect(parseManualDecisionProposal({ message: 'Proposal', draft })).toBeNull();
      expect(mergeManualDecisionDraft(base(), draft)).toEqual(base());
    }
  });
  it('merges only fields the person has not edited since the question', () => {
    expect(
      mergeManualDecisionDraft(
        { ...base(), title: 'My edit' },
        { ...base(), title: 'AI title', context: 'Proposed context' },
        base(),
      ),
    ).toEqual({ ...base(), title: 'My edit', context: 'Proposed context' });
  });
  it('uses the profile calendar, never expiry, including DST', () => {
    expect(
      manualDeadlineLabel('2026-03-29', 'Europe/Copenhagen', new Date('2026-03-29T21:59:00Z')),
    ).toBe('Due 2026-03-29');
    expect(
      manualDeadlineLabel('2026-03-29', 'Europe/Copenhagen', new Date('2026-03-29T22:00:00Z')),
    ).toBe('Overdue · 2026-03-29');
  });
});
