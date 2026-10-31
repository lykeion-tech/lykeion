import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

const src = 'apps/dashboard/src';

describe('the dashboard source layout', () => {
  it('has a features folder and a ui folder', () => {
    expect(existsSync(join(src, 'features'))).toBe(true);
    expect(existsSync(join(src, 'ui'))).toBe(true);
  });
});
