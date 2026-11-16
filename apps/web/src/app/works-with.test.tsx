import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it } from 'vitest';
import Home from './page';

it('keeps compatibility icons bounded before styles load', () => {
  const html = renderToStaticMarkup(<Home />);
  const row = html.match(/<div class="works-with"[\s\S]*?<\/div>/)?.[0] ?? '';
  const icons = row.match(/<svg\b[^>]*>/g) ?? [];
  expect(icons).toHaveLength(2);
  for (const icon of icons) {
    expect(icon).toContain('width="18"');
    expect(icon).toContain('height="18"');
  }
});
