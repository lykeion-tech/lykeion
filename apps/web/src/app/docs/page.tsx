import type { Metadata } from 'next';
import { articles } from './content';

export const metadata: Metadata = { title: 'Documentation · Lykeion' };

export default function DocsPage() {
  return <main>
    <h1>Lykeion documentation</h1>
    <p>Guides for your research workspace.</p>
    <ul>{articles.map(article => <li key={article.slug}>
      <a href={`/docs/${article.slug}/`}>{article.title}</a>
      <p>{article.description}</p>
    </li>)}</ul>
  </main>;
}
