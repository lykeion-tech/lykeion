import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import SiteFooter from '../site-footer';
import { DocsShell, articleContent } from './docs-shell';
import { searchArticles } from './search';
import { articles } from './content';

describe('documentation navigation', () => {
  it('links the footer to the documentation', () => {
    expect(renderToStaticMarkup(<SiteFooter />)).toMatch(/href="\/docs\/?"/);
  });
  it('lists every published article and marks the current article', () => {
    const html = renderToStaticMarkup(<DocsShell articles={articles} currentSlug="welcome"><p>Article</p></DocsShell>);
    for (const article of articles) expect(html).toContain(`href="/docs/${article.slug}/"`);
    expect(html).toContain('aria-current="page"');
  });
  it('links the brand to overview and offers the website and repository in navigation', () => {
    const html = renderToStaticMarkup(<DocsShell articles={articles}><p>Article</p></DocsShell>);
    expect(html).toMatch(/<a\b(?=[^>]*class="docs-brand")(?=[^>]*href="\/docs\/?")[^>]*>/);
    expect(html).toContain('href="https://github.com/lykeion-tech/lykeion"');
    expect(html).toContain('href="/"');
  });
  it('derives stable unique anchors and labels from safe Markdown tokens', () => {
    const result = articleContent('# Title\n\n## A `code` heading\n\n## A `code` heading\n\n<script>unsafe</script>');
    expect(result.headings.map(heading => heading.id)).toEqual(['a-code-heading', 'a-code-heading-2']);
    expect(result.headings[0].title).toBe('A code heading');
    expect(result.html).toContain('id="a-code-heading"');
    expect(result.html).not.toContain('<script>');
    expect(articleContent('## A\n\n## A\n\n## A-2').headings.map(heading => heading.id)).toEqual(['a', 'a-2', 'a-2-2']);
  });
  it('searches titles, descriptions and Markdown and returns no unmatched articles', () => {
    for (const query of ['Welcome', 'people steer', 'Sign in']) expect(searchArticles(articles, query)).toContain(articles[0]);
    expect(searchArticles(articles, 'zzzzzz')).toEqual([]);
  });
});


it('makes fenced and indented code examples keyboard scrollable in server HTML', () => {
  for (const markdown of ['```sh\necho hello\n```', '    echo hello']) {
    expect(articleContent(markdown).html).toContain('<pre tabindex="0" role="region" aria-label="Code example">');
  }
});
