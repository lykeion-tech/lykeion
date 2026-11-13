import { describe, expect, it } from 'vitest';
import { typedWord } from '../apps/web/src/typing';

describe('hero typing animation', () => {
  it('types, holds, erases and advances to the next word', () => {
    expect(typedWord(0)).toBe('');
    expect(typedWord(450)).toBe('DISCO');
    expect(typedWord(1100)).toBe('DISCOVERY');
    expect(typedWord(3700)).toBe('');
    expect(typedWord(3800)).toBe('E');
    expect(typedWord(8500)).toBe('VERIFICATION');
    expect(typedWord(11000)).toBe('V');
    expect(typedWord(11100)).toBe('');
  });
});
