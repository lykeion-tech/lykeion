import { expect, it } from 'vitest';
import { CYCLE, HOLD, INTRO, wordmarkFrame } from './wordmark';

const word = (t: number) => wordmarkFrame(t, () => 0).letters.join('');

it('types Lykeion in, then turns it into Λύκειον and back, forever', () => {
  expect(word(0)).toBe('L');
  expect(
    wordmarkFrame(0)
      .letters.slice(1)
      .every((l) => l === ''),
  ).toBe(true);
  expect(word(INTRO)).toBe('Lykeion');
  expect(word(INTRO + HOLD + 400)).toBe('Lyke');
  expect(word(INTRO + CYCLE - 1)).toBe('Λύκειον');
  expect(word(INTRO + CYCLE + 100)).toBe('Λύκειον');
  expect(word(INTRO + 2 * CYCLE - 1)).toBe('Lykeion');
});

it('flips each typed letter up from -90° to flat', () => {
  // The Greek word starts typing after the hold and the 7-step erase.
  const typing = INTRO + HOLD + 7 * 150;
  expect(wordmarkFrame(typing).rotation[0]).toBeCloseTo(-90);
  expect(wordmarkFrame(typing + 90).rotation[0]).toBeCloseTo(-45);
  expect(wordmarkFrame(typing + 180).rotation[0]).toBeCloseTo(0);
  expect(wordmarkFrame(typing + 90).letters[0]).toBe('Λ');
});
