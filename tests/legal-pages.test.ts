import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const require = createRequire(import.meta.url);
const { parse } = require('../apps/web/node_modules/next/dist/compiled/node-html-parser');
const expected = {
  privacy: [
    'f20815c9d80d5affd306c20e1ca36b32f7ea80701a18e0d38878d6e93e8b54e1',
    'Privacy Policy · Lykeion',
  ],
  terms: [
    'a82699248dd45742167b300aa8441900ceb307dffb9fc4bdf423cbc9f000f682',
    'Terms of Service · Lykeion',
  ],
} as const;

describe('exported legal pages', () => {
  for (const [name, [digest, title]] of Object.entries(expected)) {
    it(`preserves all text and links in ${name}`, () => {
      const doc = parse(readFileSync(`apps/web/out/${name}/index.html`, 'utf8'));
      expect(doc.querySelector('title')?.textContent).toBe(title);
      const main = doc.querySelector('main');
      expect(main).toBeTruthy();
      const rows = main
        .querySelectorAll('h1,h2,p,li,a')
        .map((element: any) => [
          element.tagName,
          element.textContent.replace(/\s+/g, ' ').trim(),
          element.tagName === 'A' ? (element.getAttribute('href') ?? '') : '',
        ]);
      expect(createHash('sha256').update(JSON.stringify(rows)).digest('hex')).toBe(digest);
    });
  }
  it('links both policies from the landing page', () => {
    const doc = parse(readFileSync('apps/web/out/index.html', 'utf8'));
    const footer = doc.querySelector('footer');
    expect(footer.querySelector('a[href="/privacy/"]')?.textContent).toBe('Privacy');
    expect(footer.querySelector('a[href="/terms/"]')?.textContent).toBe('Terms');
  });
});
