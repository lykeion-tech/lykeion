'use client';

import Link from 'next/link';
import { useEffect, useState } from 'react';
import { loadSession } from '../../bridge';
import GetStarted from '../get-started';
import SiteFooter from '../site-footer';

export default function CompareShell({ children }: { children: React.ReactNode }) {
  const [cta, setCta] = useState('/app/#/signin');
  useEffect(() => { void loadSession().then(({ user, destination }) => { if (user) setCta(destination ?? '/app/'); }).catch(() => {}); }, []);
  return <><header className="site-header"><div className="nav-inner"><Link className="brand" href="/" aria-label="Lykeion home"><img src="/lykeion.svg" alt=""/></Link><div className="nav-actions"><a className="github" href="https://github.com/lykeion-tech/lykeion"><img src="/github.svg" width="17" height="17" alt=""/> GitHub</a><a className="nav-login" href={cta}>Log in</a><a className="pill primary" href={cta}>Get started</a></div></div></header><main>{children}<div className="comparison-cta"><GetStarted cta={cta} showDemo={false}/></div></main><SiteFooter/></>;
}
