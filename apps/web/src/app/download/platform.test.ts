import { describe, expect, it } from 'vitest';
import { downloadPlatform } from './platform';

describe('download platform', () => {
  it('matches supported operating systems and architectures without guessing', () => {
    expect(downloadPlatform('macOS', 'arm')).toBe('mac-arm64');
    expect(downloadPlatform('macOS', 'x86')).toBe('mac-x64');
    expect(downloadPlatform('Linux', 'aarch64')).toBe('linux-arm64');
    expect(downloadPlatform('Linux', 'x86_64')).toBe('linux-x64');
    expect(downloadPlatform('macOS', '')).toBeNull();
    expect(downloadPlatform('Windows', 'x86')).toBeNull();
    expect(downloadPlatform('Linux', 'unknown')).toBeNull();
  });
});
