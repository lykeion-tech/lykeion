import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';

const root = 'apps/dashboard/src';
const files = (dir: string): string[] =>
  readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    return statSync(path).isDirectory() ? files(path) : /\.(ts|vue)$/.test(name) ? [path] : [];
  });

describe('the dashboard reads the server, not a mock', () => {
  it('has no mock service or mock contract', () => {
    expect(existsSync(join(root, 'services'))).toBe(false);
    expect(existsSync('packages/contracts/src/research.ts')).toBe(false);
  });
  it('compiles no file that imports one', () => {
    const offenders = files(root)
      .filter((f) => /services\/(workspace|research)|contracts\/src\/research/.test(readFileSync(f, 'utf8')));
    expect(offenders).toEqual([]);
  });
});
