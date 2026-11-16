'use client';

import { useEffect, useState } from 'react';

export function TableOfContents({ headings }: { headings: { id: string; title: string; level: string }[] }) {
  const [active, setActive] = useState<string | undefined>(headings[0]?.id);
  useEffect(() => {
    const update = () => {
      if (window.scrollY + window.innerHeight >= document.documentElement.scrollHeight - 2) {
        setActive(headings.at(-1)?.id);
        return;
      }
      const current = headings.filter(heading => {
        const element = document.getElementById(heading.id);
        return element && element.getBoundingClientRect().top <= 120;
      }).at(-1);
      setActive(current?.id ?? headings[0]?.id);
    };
    update();
    window.addEventListener('scroll', update, { passive: true });
    return () => window.removeEventListener('scroll', update);
  }, [headings]);
  return <aside className="docs-toc"><nav aria-label="On this page"><h2>On this page</h2><div className="docs-toc-links">{headings.map(heading => <a key={heading.id} className={heading.level === 'h3' ? 'docs-toc-sub' : ''} href={`#${heading.id}`} aria-current={active === heading.id ? 'location' : undefined}>{heading.title}</a>)}</div></nav></aside>;
}
