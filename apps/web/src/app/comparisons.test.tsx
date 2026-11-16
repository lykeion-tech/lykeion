import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it } from 'vitest';
import SiteFooter from './site-footer';

it('links to each competitor comparison from the footer', () => {
  const html = renderToStaticMarkup(<SiteFooter />);
  for (const slug of ['claude-science', 'openscience', 'k-dense-byok', 'elicit']) {
    expect(html).toMatch(new RegExp(`href="/compare/${slug}/?"`));
  }
});

it('links to the About page from the footer', () => {
  expect(renderToStaticMarkup(<SiteFooter />)).toMatch(/href="\/about\/?"/);
});
