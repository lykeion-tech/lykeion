import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { ArticleActions } from './article-actions';
import { getArticle } from './content';

describe('article actions', () => {
  it('offers copy actions and the canonical exported Markdown URL', () => {
    const article = getArticle('welcome')!;
    const html = renderToStaticMarkup(<ArticleActions article={article} />);
    expect(html).toContain('Copy page');
    expect(html).toContain('Copy Markdown link');
    expect(html).toContain(`href="https://app.lykeion.tech/docs/${article.slug}/markdown/"`);
    expect(html).toContain('View Markdown');
    for (const excluded of ['MCP', 'Cursor', 'VS Code']) expect(html).not.toContain(excluded);
  });
});
