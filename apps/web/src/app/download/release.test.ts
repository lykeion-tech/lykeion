import { describe, expect, it } from 'vitest';
import { parseRelease } from './release';

describe('desktop release', () => {
  it('uses only uploaded assets from the current release', () => {
    const result = parseRelease({ tag_name: 'v0.1.1', published_at: '2026-11-17T06:25:07Z', assets: [
      { name: 'Lykeion-0.1.1-arm64.dmg', state: 'uploaded' },
      { name: 'lykeion_0.1.1_amd64.deb', state: 'uploaded' },
      { name: 'Lykeion-0.1.1.dmg', state: 'new' },
    ] });
    expect(result?.tag).toBe('v0.1.1');
    expect(result?.files['mac-arm64']?.['.dmg']).toContain('/v0.1.1/Lykeion-0.1.1-arm64.dmg');
    expect(result?.files['linux-x64']?.['.deb']).toContain('lykeion_0.1.1_amd64.deb');
    expect(result?.files['mac-x64']?.['.dmg']).toBeUndefined();
  });
  it('rejects invalid releases', () => {
    expect(parseRelease({ tag_name: '../bad', published_at: 'invalid', assets: [] })).toBeNull();
    expect(parseRelease(null)).toBeNull();
  });
});
