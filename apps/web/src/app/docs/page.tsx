import type { Metadata } from 'next';
import { articles } from './content';
import { DocsShell } from './docs-shell';
import { ArrowUpRight } from 'lucide-react';

export const metadata: Metadata = { title: 'Documentation · Lykeion' };

export default function DocsPage() {
  return <DocsShell articles={articles}>
    <div className="docs-index-intro"><p className="docs-eyebrow">Lykeion docs</p>
    <h1>Lykeion documentation</h1>
    <p>Guides for your research workspace.</p>
    </div><div className="docs-guide-grid">{articles.map(article => <a className="docs-guide" key={article.slug} href={`/docs/${article.slug}/`}>
      <span className="docs-guide-section">{article.section} <ArrowUpRight size={12} aria-hidden="true" /></span><h2>{article.title}</h2>
      <p>{article.description}</p>
    </a>)}</div>
  </DocsShell>;
}
