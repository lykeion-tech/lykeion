import Link from 'next/link';
import type { ReactNode } from 'react';
import MarkdownIt from 'markdown-it';
import type { DocArticle } from './content';
import { DocsSearch } from './search';
import { TableOfContents } from './table-of-contents';
import { ThemePicker } from './theme-picker';
import { ArrowUpRight } from 'lucide-react';
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
  const navigation = <nav aria-label="Documentation"><div className="docs-site-links"><a href="https://github.com/lykeion-tech/lykeion"><img src="/github.svg" alt="" width="16" height="16" />GitHub <ArrowUpRight size={12} aria-hidden="true" /></a><Link href="/"><img src="/lykeion.svg" alt="" width="16" height="16" />Lykeion <ArrowUpRight size={12} aria-hidden="true" /></Link></div>{sections.map(section => <div className="docs-nav-group" key={section}><h2>{section}</h2>{section === 'Get started' && <Link className="docs-overview" href="/docs/" aria-current={!currentSlug ? 'page' : undefined}>Overview</Link>}{articles.filter(article => article.section === section).map(article => <a key={article.slug} href={`/docs/${article.slug}/`} aria-current={article.slug === currentSlug ? 'page' : undefined}>{article.slug === 'welcome' ? 'Welcome' : article.title}</a>)}</div>)}</nav>;
  return <><script dangerouslySetInnerHTML={{ __html: `(function(){var theme;try{theme=localStorage.getItem('lykeion-docs-theme')}catch(e){}var dark=theme==='dark'||(theme!=='light'&&matchMedia('(prefers-color-scheme: dark)').matches);document.documentElement.dataset.docsTheme=dark?'dark':'light'})()` }} /><div className="docs-site">
    <a className="docs-skip" href="#docs-content">Skip to content</a>
    <header className="docs-header"><Link href="/docs/" className="docs-brand"><img src="/lykeion.svg" alt="" width="30" height="30" /><span>Lykeion</span></Link><Link href="/docs/" className="docs-label">Docs</Link><DocsSearch articles={articles} /></header>
    <div className="docs-layout">
      <aside className="docs-sidebar"><div className="docs-desktop-nav">{navigation}</div><details className="docs-mobile-nav"><summary>Explore documentation</summary>{navigation}</details><ThemePicker /></aside>
      <main id="docs-content" className="docs-content"><div className="docs-breadcrumb"><Link href="/docs/">Documentation</Link><span>/</span><span>{current?.section || 'Get started'}</span></div>{children}<div className="docs-article-foot">Research, better together.</div></main>
      {headings.length > 0 && <TableOfContents headings={headings} />}
    </div>
  </div></>;
}
