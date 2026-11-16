'use client';

import { useState } from 'react';
import { ChevronDown, Copy, FileText, Link2 } from 'lucide-react';
import { cliIconPaths } from '../../../../../packages/cli-icons';
import type { DocArticle } from './content';

export function ArticleActions({ article }: { article: DocArticle }) {
  const [status, setStatus] = useState('');
  const markdownUrl = `https://app.lykeion.tech/docs/${encodeURIComponent(article.slug)}/markdown/`;
  const prompt = encodeURIComponent(`Read https://app.lykeion.tech/docs/${encodeURIComponent(article.slug)}/ so I can ask questions about it.`);
  async function copy(text: string, success: string) {
    try {
      await navigator.clipboard.writeText(text);
      setStatus(success);
    } catch {
      setStatus('Could not copy. Open View Markdown and copy the text or URL from your browser.');
    }
  }
  return <div role="group" aria-label="Article actions" className="docs-actions">
    <div className="docs-actions-control"><button type="button" className="docs-copy-page" onClick={() => copy(article.markdown, 'Page copied.')}><Copy size={15} aria-hidden="true" />Copy page</button><details>
      <summary aria-label="More page actions"><ChevronDown size={15} aria-hidden="true" /></summary>
      <div className="docs-actions-list">
        <button type="button" onClick={() => copy(markdownUrl, 'Markdown link copied.')}><Link2 size={15} aria-hidden="true" />Copy Markdown link</button>
        <a href={markdownUrl} target="_blank" rel="noopener noreferrer"><FileText size={15} aria-hidden="true" />View Markdown</a>
        <a href={`https://chatgpt.com/?hints=search&q=${prompt}`} target="_blank" rel="noopener noreferrer"><svg viewBox="0 0 24 24" width="15" height="15" fill="currentColor" aria-hidden="true"><path d={cliIconPaths.codex} /></svg>Open in ChatGPT</a>
        <a href={`https://claude.ai/new?q=${prompt}`} target="_blank" rel="noopener noreferrer"><svg viewBox="0 0 24 24" width="15" height="15" fill="#D97757" aria-hidden="true"><path d={cliIconPaths.claude} /></svg>Open in Claude</a>
      </div>
    </details></div>
    <p role="status">{status}</p>
  </div>;
}
