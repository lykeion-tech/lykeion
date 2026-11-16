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
  const html = articleContent(article.proseMarkdown ?? article.markdown).html;
  const learning = article.slug === 'core-concepts' ? <ConceptsDiagram />
    : article.slug === 'scientific-stages' ? <StudyWalkthrough /> : null;
  const introEnd = learning ? html.indexOf('<h2') : -1;
  return <DocsShell articles={articles} currentSlug={article.slug}>
    <ArticleActions article={article} />
    {introEnd >= 0 ? <article>
      <div className="docs-prose" dangerouslySetInnerHTML={{ __html: html.slice(0, introEnd) }} />
      {learning}
      <div className="docs-prose" dangerouslySetInnerHTML={{ __html: html.slice(introEnd) }} />
    </article> : <article className="docs-prose" dangerouslySetInnerHTML={{ __html: html }} />}
  </DocsShell>;
}
