import { expect, it } from 'vitest';
import { CYCLE, HOLD, wordmarkFrame } from './wordmark';

const word = (t: number) => wordmarkFrame(t).letters.join('');

it('opens on Lykeion, holds it, then turns it into Λύκειον and back, forever', () => {
  expect(word(0)).toBe('Lykeion');
  expect(word(HOLD - 1)).toBe('Lykeion');
  expect(word(HOLD + 400)).toBe('Lyke');
  expect(word(CYCLE - 1)).toBe('Λύκειον');
  expect(word(CYCLE + 100)).toBe('Λύκειον');
  expect(word(2 * CYCLE - 1)).toBe('Lykeion');
});

it('flips each typed letter up from -90° to flat', () => {
  // The Greek word starts typing after the hold and the 7-step erase.
  const typing = HOLD + 7 * 150;
  expect(wordmarkFrame(typing).rotation[0]).toBeCloseTo(-90);
  expect(wordmarkFrame(typing + 90).rotation[0]).toBeCloseTo(-45);
  expect(wordmarkFrame(typing + 180).rotation[0]).toBeCloseTo(0);
  expect(wordmarkFrame(typing + 90).letters[0]).toBe('Λ');
});
