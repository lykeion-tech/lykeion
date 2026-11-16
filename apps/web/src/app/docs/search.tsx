'use client';

import { useEffect, useRef, useState } from 'react';
import type { DocArticle } from './content';

export function searchArticles(articles: DocArticle[], query: string) {
  const terms = query.toLowerCase().trim().split(/\s+/);
  return articles.filter(article => terms.every(term => `${article.title} ${article.description} ${article.markdown}`.toLowerCase().includes(term)));
}

export function DocsSearch({ articles }: { articles: DocArticle[] }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const [query, setQuery] = useState('');
  function open() { dialog.current?.showModal(); input.current?.focus(); }
  useEffect(() => {
    function shortcut(event: KeyboardEvent) {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') { event.preventDefault(); dialog.current?.showModal(); input.current?.focus(); }
    }
    document.addEventListener('keydown', shortcut);
    return () => document.removeEventListener('keydown', shortcut);
  }, []);
  const results = searchArticles(articles, query);
  return <>
    <button ref={trigger} className="docs-search-trigger" onClick={open}>Search documentation <kbd>⌘ / Ctrl K</kbd></button>
    <dialog ref={dialog} className="docs-search" aria-labelledby="docs-search-title" onKeyDown={event => { if (event.key === 'Escape') { event.preventDefault(); dialog.current?.close(); } }} onClose={() => trigger.current?.focus()}>
      <div className="docs-search-top"><h2 id="docs-search-title">Search documentation</h2><button onClick={() => dialog.current?.close()} aria-keyshortcuts="Escape">Close search <kbd>Esc</kbd></button></div>
      <label htmlFor="docs-query">Find a guide</label>
      <input ref={input} id="docs-query" type="search" value={query} placeholder="Search topics, questions, concepts…" onChange={event => setQuery(event.target.value)} onKeyDown={event => { if (event.key === 'ArrowDown') { event.preventDefault(); dialog.current?.querySelector<HTMLAnchorElement>('.docs-search-results a')?.focus(); } }} />
      <p role="status">{results.length ? `${results.length} ${results.length === 1 ? 'guide' : 'guides'} found` : 'No results. Try another search.'}</p>
      <ul className="docs-search-results">{results.map(article => <li key={article.slug}><a href={`/docs/${article.slug}/`}><strong>{article.title}</strong><span>{article.description}</span></a></li>)}</ul>
    </dialog>
  </>;
}
