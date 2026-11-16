'use client';

import { useState } from 'react';
import { ChevronDown, Copy, ExternalLink, Link2 } from 'lucide-react';
import type { DocArticle } from './content';

export function ArticleActions({ article }: { article: DocArticle }) {
  const [status, setStatus] = useState('');
  const markdownUrl = `https://app.lykeion.tech/docs/${encodeURIComponent(article.slug)}/markdown/`;
  async function copy(text: string, success: string) {
    try {
      await navigator.clipboard.writeText(text);
      setStatus(success);
    } catch {
      setStatus('Could not copy. Open View Markdown and copy the text or URL from your browser.');
    }
  }
  return <div role="group" aria-label="Article actions" className="docs-actions">
    <details>
      <summary>Page actions<ChevronDown size={15} aria-hidden="true" /></summary>
      <div className="docs-actions-list">
        <button type="button" onClick={() => copy(article.markdown, 'Page copied.')}><Copy size={15} aria-hidden="true" />Copy page</button>
        <button type="button" onClick={() => copy(markdownUrl, 'Markdown link copied.')}><Link2 size={15} aria-hidden="true" />Copy Markdown link</button>
        <a href={markdownUrl} target="_blank" rel="noopener noreferrer"><ExternalLink size={15} aria-hidden="true" />View Markdown</a>
      </div>
    </details>
    <p role="status">{status}</p>
  </div>;
}
