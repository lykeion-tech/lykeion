import { describe, expect, it } from 'vitest';
import { articles, getArticle } from './content';
import { renderMarkdown } from './markdown';
import ArticlePage, { generateStaticParams as articleParams } from './[slug]/page';
import { GET, generateStaticParams as markdownParams } from './[slug]/markdown/route';

describe('documentation source and static routes', () => {
  it('has a nonempty registry with unique slugs and exact lookups', () => {
    expect(articles.length).toBeGreaterThan(0);
    expect(new Set(articles.map(article => article.slug)).size).toBe(articles.length);
    for (const article of articles) expect(getArticle(article.slug)).toBe(article);
    expect(getArticle('missing-article')).toBeUndefined();
  });
  it('generates every article and Markdown route', () => {
    const params = articles.map(({ slug }) => ({ slug }));
    expect(articleParams()).toEqual(params);
    expect(markdownParams()).toEqual(params);
  });
  it('exports the exact canonical Markdown with a text content type', async () => {
    for (const article of articles) {
      const response = await GET(new Request('https://example.com'), { params: Promise.resolve({ slug: article.slug }) });
      expect(response.headers.get('content-type')).toBe('text/markdown; charset=utf-8');
      expect(await response.text()).toBe(article.markdown);
    }
  });
  it('rejects unknown article and Markdown routes', async () => {
    const props = { params: Promise.resolve({ slug: 'missing-article' }) };
    await expect(ArticlePage(props)).rejects.toThrow('NEXT_HTTP_ERROR_FALLBACK;404');
    expect((await GET(new Request('https://example.com'), props)).status).toBe(404);
  });
  it('escapes raw HTML while rendering links and fenced code', () => {
    const html = renderMarkdown('<script>alert(1)</script>\n\n[Welcome](/docs/welcome/)\n\n```sh\nlyk --help\n```');
    expect(html).not.toContain('<script>');
    expect(html).toContain('&lt;script&gt;');
    expect(html).toContain('<a href="/docs/welcome/">Welcome</a>');
    expect(html).toContain('<pre><code class="language-sh">lyk --help\n</code></pre>');
    expect(renderMarkdown('[unsafe](javascript:alert(1))')).not.toContain('href="javascript:');
  });
});
