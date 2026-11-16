import type { Metadata } from 'next';
import { notFound } from 'next/navigation';
import { articles, getArticle } from '../content';
import { ConceptsDiagram } from '../concepts-diagram';
import { StudyWalkthrough } from '../study-walkthrough';
import { ArticleActions } from '../article-actions';
import { DocsShell, articleContent } from '../docs-shell';

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
  return <DocsShell articles={articles} currentSlug={article.slug}>
    <ArticleActions article={article} />
    {article.slug === 'core-concepts' && <ConceptsDiagram />}
    {article.slug === 'scientific-stages' && <StudyWalkthrough />}
    <article className="docs-prose" dangerouslySetInnerHTML={{ __html: articleContent(article.markdown).html }} />
  </DocsShell>;
}
