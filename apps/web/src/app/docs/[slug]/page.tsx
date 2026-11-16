import type { Metadata } from 'next';
import { notFound } from 'next/navigation';
import { articles, getArticle } from '../content';
import { renderMarkdown } from '../markdown';

type Props = { params: Promise<{ slug: string }> };

export const dynamicParams = false;

export function generateStaticParams() {
  return articles.map(({ slug }) => ({ slug }));
}

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const article = getArticle((await params).slug);
  if (!article) notFound();
  return { title: `${article.title} · Lykeion`, description: article.description };
}

export default async function ArticlePage({ params }: Props) {
  const article = getArticle((await params).slug);
  if (!article) notFound();
  return <main>
    <nav aria-label="Documentation"><a href="/docs/">Documentation</a></nav>
    <article dangerouslySetInnerHTML={{ __html: renderMarkdown(article.markdown) }} />
  </main>;
}
