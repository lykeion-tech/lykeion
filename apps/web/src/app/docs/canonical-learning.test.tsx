import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { articles, getArticle } from './content';
import { searchArticles } from './search';
import { GET } from './[slug]/markdown/route';
import ArticlePage from './[slug]/page';
import { articleContent } from './docs-shell';
import { stages, concepts, connections } from './learning-content';

describe('canonical interactive learning content', () => {
  it('makes exact gates and validation routing searchable and exportable', async () => {
    const article = getArticle('scientific-stages')!;
    expect(searchArticles(articles, 'falsifiability')).toContain(article);
    expect(article.markdown).toContain(
      'The server checks recorded citations; Jev checks falsifiability, evidence support and whether the exact prediction was already tested.',
    );
    expect(article.markdown).toContain('back to Experimentation once');
    for (const stage of stages) {
      for (const text of [stage.input, stage.agents, stage.evidence, stage.gate, stage.result])
        expect(article.markdown).toContain(text);
    }
    const response = await GET(new Request('https://example.com'), {
      params: Promise.resolve({ slug: article.slug }),
    });
    expect(await response.text()).toBe(article.markdown);
  });
  it('exports concept descriptions and both connection directions', () => {
    const markdown = getArticle('core-concepts')!.markdown;
    expect(markdown).toContain('A reusable research role with instructions');
    for (const [index, concept] of concepts.entries()) {
      expect(markdown).toContain(concept.text);
      expect(markdown).toContain(
        connections(index)
          .map((target) => concepts[target].name)
          .join(' · '),
      );
    }
    expect(markdown.split('### Workspace')[1].split('###')[0]).toContain('Agent');
    expect(markdown.split('### Agent')[1].split('###')[0]).toContain('Workspace');
    expect(markdown.split('### Study')[1].split('###')[0]).toContain('Aristotle');
    expect(markdown.split('### Aristotle')[1]).toContain('Study');
  });
  it('renders one graphic equivalent without duplicate prose and preserves safe Markdown', async () => {
    const html = renderToStaticMarkup(
      await ArticlePage({ params: Promise.resolve({ slug: 'scientific-stages' }) }),
    );
    expect(html.match(/The server checks recorded citations;/g)).toHaveLength(1);
    expect(html.match(/<h1/g)).toHaveLength(1);
    expect(html).not.toContain('href="#stage-walkthrough"');
    expect(html).toContain('href="#read-the-stage-vocabulary"');
    const conceptHtml = renderToStaticMarkup(
      await ArticlePage({ params: Promise.resolve({ slug: 'core-concepts' }) }),
    );
    expect(conceptHtml.match(/A reusable research role with instructions/g)).toHaveLength(1);
    expect(conceptHtml).not.toContain('href="#concept-relationships"');
    expect(
      articleContent(getArticle('scientific-stages')!.markdown + '\n<script>alert(1)</script>')
        .html,
    ).not.toContain('<script>');
  });
});

it('preserves the concept relationship caveat in canonical Markdown and the graphic', async () => {
  const note =
    'Lines show documented relationships, not automatic data flow or guaranteed execution.';
  expect(getArticle('core-concepts')!.markdown).toContain(note);
  const html = renderToStaticMarkup(
    await ArticlePage({ params: Promise.resolve({ slug: 'core-concepts' }) }),
  );
  expect(
    html.match(
      /Lines show documented relationships, not automatic data flow or guaranteed execution\./g,
    ),
  ).toHaveLength(1);
});
