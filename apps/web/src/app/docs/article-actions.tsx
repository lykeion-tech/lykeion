'use client';

import { useState } from 'react';
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
  return <div role="group" aria-label="Article actions" style={{ marginBlock: '1rem' }}>
    <details>
      <summary>Page actions</summary>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: '1rem', paddingBlock: '1rem' }}>
        <button type="button" onClick={() => copy(article.markdown, 'Page copied.')}>Copy page</button>
        <button type="button" onClick={() => copy(markdownUrl, 'Markdown link copied.')}>Copy Markdown link</button>
        <a href={markdownUrl} target="_blank" rel="noopener noreferrer">View Markdown</a>
      </div>
    </details>
    <p role="status">{status}</p>
  </div>;
}
