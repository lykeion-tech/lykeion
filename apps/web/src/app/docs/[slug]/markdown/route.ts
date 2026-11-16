import { articles, getArticle } from '../../content';

export const dynamic = 'force-static';
export const dynamicParams = false;

export function generateStaticParams() {
  return articles.map(({ slug }) => ({ slug }));
}

export async function GET(_request: Request, { params }: { params: Promise<{ slug: string }> }) {
  const article = getArticle((await params).slug);
  if (!article) return new Response('Article not found', { status: 404 });
  return new Response(article.markdown, {
    headers: { 'Content-Type': 'text/markdown; charset=utf-8' },
  });
}
