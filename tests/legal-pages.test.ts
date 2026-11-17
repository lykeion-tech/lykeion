import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const require = createRequire(import.meta.url);
const { parse } = require('../apps/web/node_modules/next/dist/compiled/node-html-parser');
const expected = {
  privacy: [
    '06c71d47a572412e85c4841cd97f8b29fd3286d6425e9a6692ea0b06cb0e66f4',
    'Privacy Policy · Lykeion',
  ],
  terms: [
    '56cac9ea1a8a24efe38a290d12b8550532a3630c126e33207064445227aac032',
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
