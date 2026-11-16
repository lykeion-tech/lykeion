import type { Metadata } from 'next';
import { articles } from './content';
import { DocsShell } from './docs-shell';

export const metadata: Metadata = { title: 'Documentation · Lykeion' };

export default function DocsPage() {
  return <DocsShell articles={articles}>
    <div className="docs-index-intro"><p className="docs-eyebrow">THE LYKEION HANDBOOK</p>
    <h1>Lykeion documentation</h1>
    <p>Guides for your research workspace.</p>
    </div><div className="docs-guide-grid">{articles.map(article => <a className="docs-guide" key={article.slug} href={`/docs/${article.slug}/`}>
      <span className="docs-guide-section">{article.section} ↗</span><h2>{article.title}</h2>
      <p>{article.description}</p>
    </a>)}</div>
  </DocsShell>;
}
