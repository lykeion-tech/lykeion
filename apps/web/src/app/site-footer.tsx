import Link from 'next/link';
import { comparisons } from './compare/content';

export default function SiteFooter() {
  return <footer className="footer">
      <div className="footer-main wrap">
        <div className="footer-intro"><strong>Lykeion</strong><p>A research lab where agents work<br/>and people steer.</p></div>
        <nav id="compare" className="footer-columns" aria-label="Footer navigation">
          <div><h2>Compare</h2>{Object.entries(comparisons).map(([slug, item]) => <Link className="footer-compare-link" href={`/compare/${slug}/`} key={slug}>vs. <img className={slug === 'openscience' ? 'footer-compare-dark-icon' : ''} src={item.icon} width="16" height="16" alt=""/> {item.name}</Link>)}</div>
          <div><h2>Community</h2><span aria-disabled="true">Forum</span><span aria-disabled="true">Discord</span></div>
          <div><h2>Developers</h2><span aria-disabled="true">Documentation</span><a href="https://github.com/lykeion-tech/lykeion">GitHub</a><a href="https://github.com/lykeion-tech/lykeion/contribute">Contributing</a><a href="/download/">Download</a></div>
          <div><h2>Company</h2><Link href="/about/">About</Link><a href="/contact-sales/">Contact</a></div>
        </nav>
      </div>
      <div className="footer-bottom"><div className="wrap footer-bottom-inner"><div className="footer-copyright"><img src="/lykeion.svg" alt=""/><span aria-hidden="true">{'//'}</span><p>Copyright © 2026 Lykeion.<br/>All rights reserved.</p></div><div className="footer-bottom-links"><div><a href="/privacy/">Privacy</a><a href="/terms/">Terms</a></div><a href="https://github.com/lykeion-tech/lykeion" aria-label="Lykeion on GitHub"><img src="/github.svg" width="15" height="15" alt=""/></a></div></div></div>
    </footer>;
}
