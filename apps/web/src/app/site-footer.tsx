import Link from 'next/link';
import { comparisons } from './compare/content';

export default function SiteFooter() {
  return <footer className="footer">
      <div className="footer-main wrap">
        <div className="footer-intro"><strong>Lykeion</strong><p>A research lab where agents work<br/>and people steer.</p></div>
        <nav id="compare" className="footer-columns" aria-label="Footer navigation">
          <div><h2>Compare</h2>{Object.entries(comparisons).map(([slug, item]) => <Link className="footer-compare-link" href={`/compare/${slug}/`} key={slug}>vs. <img className={slug === 'openscience' ? 'footer-compare-dark-icon' : ''} src={item.icon} width="16" height="16" alt=""/> {item.name}</Link>)}</div>
          <div><h2>Community</h2><a href="https://discord.gg/ed3jsMTs3">Discord</a><a href="https://github.com/lykeion-tech/lykeion/contribute">Contributing</a></div>
          <div><h2>Developers</h2><Link href="/docs/">Documentation</Link><a href="https://github.com/lykeion-tech/lykeion">GitHub</a><a href="/download/">Download</a></div>
          <div><h2>Company</h2><Link href="/about/">About</Link><a href="/contact-sales/">Contact</a></div>
        </nav>
      </div>
      <div className="footer-bottom"><div className="wrap footer-bottom-inner"><div className="footer-copyright"><img src="/lykeion.svg" alt=""/><span className="footer-slashes" aria-hidden="true"><span>/</span><span>/</span></span><p>Copyright © 2026 Lykeion.<br/>All rights reserved.</p></div><div className="footer-bottom-links"><div><a href="/privacy/">Privacy</a><a href="/terms/">Terms</a></div><div className="footer-social"><a href="https://github.com/lykeion-tech/lykeion" aria-label="Lykeion on GitHub"><img src="/github.svg" width="24" height="24" alt=""/></a><a href="https://discord.gg/ed3jsMTs3" aria-label="Lykeion on Discord"><img src="/discord.svg" width="24" height="24" alt=""/></a></div></div></div></div>
    </footer>;
}
