import Link from 'next/link';
import type { ReactNode } from 'react';
import MarkdownIt from 'markdown-it';
import type { DocArticle } from './content';
import { DocsSearch } from './search';
import './docs.css';

// The same tokens provide article anchors and the table of contents.
export function articleContent(markdown: string) {
  const renderer = new MarkdownIt({ html: false });
  const headings: { id: string; title: string; level: string }[] = [];
  const used = new Set<string>();
  renderer.renderer.rules.heading_open = (tokens, index, options, _env, self) => {
    const token = tokens[index];
    const title = tokens[index + 1].children?.filter(child => child.type === 'text' || child.type === 'code_inline').map(child => child.content).join('') || tokens[index + 1].content;
    const base = title.toLowerCase().replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-|-$/g, '') || 'section';
    let id = base;
    let suffix = 2;
    while (used.has(id)) id = `${base}-${suffix++}`;
    used.add(id);
    token.attrSet('id', id);
    if (token.tag !== 'h1') headings.push({ id, title, level: token.tag });
    return self.renderToken(tokens, index, options);
  };
  // Keep code scrollable by keyboard even before hydration or with JavaScript disabled.
  for (const rule of ['fence', 'code_block']) {
    const render = renderer.renderer.rules[rule]!;
    renderer.renderer.rules[rule] = (...args) => render(...args).replace('<pre>', '<pre tabindex="0" role="region" aria-label="Code example">');
  }
  const html = renderer.render(markdown);
  return { html, headings };
}

export function DocsShell({ children, currentSlug, articles }: { children: ReactNode; currentSlug?: string; articles: DocArticle[] }) {
  const sections = [...new Set(articles.map(article => article.section))];
  const current = articles.find(article => article.slug === currentSlug);
  const headings = current ? articleContent(current.proseMarkdown ?? current.markdown).headings : [];
  const navigation = <nav aria-label="Documentation"><Link className="docs-overview" href="/docs/" aria-current={!currentSlug ? 'page' : undefined}>Overview</Link>{sections.map(section => <div className="docs-nav-group" key={section}><h2>{section}</h2>{articles.filter(article => article.section === section).map(article => <a key={article.slug} href={`/docs/${article.slug}/`} aria-current={article.slug === currentSlug ? 'page' : undefined}>{article.title}</a>)}</div>)}</nav>;
  return <div className="docs-site">
    <a className="docs-skip" href="#docs-content">Skip to content</a>
    <header className="docs-header"><Link href="/" className="docs-brand"><img src="/lykeion.svg" alt="" width="30" height="30" />Lykeion</Link><Link href="/docs/" className="docs-label">Docs</Link><DocsSearch articles={articles} /><a className="docs-app-link" href="/app/">Open Lykeion ↗</a></header>
    <div className="docs-layout">
      <aside className="docs-sidebar"><div className="docs-desktop-nav">{navigation}</div><details className="docs-mobile-nav"><summary>Explore documentation</summary>{navigation}</details></aside>
      <main id="docs-content" className="docs-content"><div className="docs-breadcrumb"><Link href="/docs/">Documentation</Link><span>/</span><span>{current?.section || 'Get started'}</span></div>{children}<div className="docs-article-foot">Research, better together.<a href="/app/">Open your workspace ↗</a></div></main>
      {headings.length > 0 && <aside className="docs-toc"><nav aria-label="On this page"><h2>On this page</h2>{headings.map(heading => <a key={heading.id} className={heading.level === 'h3' ? 'docs-toc-sub' : ''} href={`#${heading.id}`}>{heading.title}</a>)}</nav></aside>}
    </div>
  </div>;
}
