export default function SiteFooter() {
  return <footer className="footer">
      <div className="footer-main wrap">
        <div className="footer-intro"><strong>Lykeion</strong><p>A research lab where agents work<br/>and people steer.</p></div>
        <nav className="footer-columns" aria-label="Footer navigation">
          <div><h2>Compare</h2><span aria-disabled="true">vs. Claude Science</span><span aria-disabled="true">vs. Open Science</span><span aria-disabled="true">vs. K-Dense BYOK</span><span aria-disabled="true">vs. Elicit</span></div>
          <div><h2>Community</h2><span aria-disabled="true">Forum</span><span aria-disabled="true">Discord</span></div>
          <div><h2>Developers</h2><span aria-disabled="true">Documentation</span><a href="https://github.com/lykeion-tech/lykeion">GitHub</a><a href="https://github.com/lykeion-tech/lykeion/contribute">Contributing</a></div>
          <div><h2>Company</h2><span aria-disabled="true">About</span><a href="/contact-sales/">Contact</a><a href="/download/">Download</a></div>
        </nav>
      </div>
      <div className="footer-bottom"><div className="wrap footer-bottom-inner"><div className="footer-copyright"><img src="/lykeion.svg" alt=""/><span aria-hidden="true">{'//'}</span><p>Copyright © 2026 Lykeion.<br/>All rights reserved.</p></div><div className="footer-bottom-links"><div><a href="/privacy/">Privacy</a><a href="/terms/">Terms</a></div><a href="https://github.com/lykeion-tech/lykeion" aria-label="Lykeion on GitHub"><img src="/github.svg" width="15" height="15" alt=""/></a></div></div></div>
    </footer>;
}
