'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import SiteFooter from '../site-footer';
import RemoteCLI from './remote-cli';
import { ArrowUpRight, Download, Monitor } from 'lucide-react';
import { downloadPlatform, type DownloadPlatform } from './platform';
import './download.css';
import { parseRelease, releaseApi, releasesUrl as release } from './release';

const downloads: { id: DownloadPlatform; name: string; formats: string[] }[] = [
  { id: 'mac-arm64', name: 'macOS Apple Silicon', formats: ['.dmg', '.zip'] },
  { id: 'mac-x64', name: 'macOS Intel', formats: ['.dmg', '.zip'] },
  { id: 'linux-x64', name: 'Linux x64', formats: ['.AppImage', '.deb'] },
  { id: 'linux-arm64', name: 'Linux ARM64', formats: ['.AppImage', '.deb'] },
];

type ClientHints = { platform?: string; getHighEntropyValues?: (hints: string[]) => Promise<{ platform?: string; architecture?: string; bitness?: string }> };

export default function DownloadPage() {
  const [latest, setLatest] = useState<ReturnType<typeof parseRelease>>(null);
  useEffect(() => {
    const controller = new AbortController();
    fetch(releaseApi, { signal: controller.signal }).then(response => {
      if (!response.ok) throw new Error('Release unavailable');
      return response.json();
    }).then(data => setLatest(parseRelease(data))).catch(() => {});
    return () => controller.abort();
  }, []);
  const [current, setCurrent] = useState<DownloadPlatform | null>(null);
  useEffect(() => {
    const hints = (navigator as Navigator & { userAgentData?: ClientHints }).userAgentData;
    if (hints?.getHighEntropyValues) {
      void hints.getHighEntropyValues(['architecture', 'bitness']).then(info => {
        if (info.bitness !== '64') return;
        setCurrent(downloadPlatform(info.platform || hints.platform || '', info.architecture || ''));
      }).catch(() => {});
    } else if (/Linux/i.test(navigator.userAgent) && !/Android/i.test(navigator.userAgent)) {
      const cpu = navigator.userAgent.match(/\b(x86_64|aarch64)\b/i)?.[0] || '';
      void Promise.resolve().then(() => setCurrent(downloadPlatform('linux', cpu)));
    }
  }, []);
  const recommended = downloads.find(item => item.id === current);
  return <>
    <header className="site-header"><div className="nav-inner"><Link className="brand" href="/" aria-label="Lykeion home"><img src="/lykeion.svg" alt=""/></Link><div className="nav-actions"><a className="github" href="https://github.com/lykeion-tech/lykeion"><img src="/github.svg" alt="" width="17" height="17"/> GitHub</a><a className="nav-login" href="/app/#/signin">Log in</a><a className="pill primary" href="/app/#/signin">Get started</a></div></div></header>
    <main className="download-page"><section className="download-hero"><div className="download-wrap download-grid"><div className="download-intro"><span className="download-kicker">DESKTOP APP</span><h1>{recommended ? `Download Lykeion for ${recommended.name}` : 'Download Lykeion'}</h1><p>The desktop app comes with the agent daemon, so Claude Code and Codex on this computer can connect to your lab.</p><a className="pill primary download-main-action" href={recommended ? (latest?.files[recommended.id]?.[recommended.formats[0]] ?? release) : '#all-downloads'}><Download size={16} aria-hidden="true"/>{recommended ? `Download for ${recommended.name}` : 'Choose your download'}</a><div className="download-release">{latest && <>{latest.tag} <span>·</span> Released {new Date(latest.date).toLocaleDateString('en-US', { year: 'numeric', month: 'short', day: 'numeric', timeZone: 'UTC' })} <span>·</span></>} <a href={latest ? `${release}/tag/${latest.tag}` : release}>What’s new <ArrowUpRight size={12} aria-hidden="true"/></a></div></div><div className="download-list" id="all-downloads"><div className="download-list-head"><strong>All downloads</strong><a href={release}>All releases <ArrowUpRight size={12} aria-hidden="true"/></a></div>{downloads.map(item => <div className={`download-row${current === item.id ? ' current' : ''}`} key={item.id}><div className="download-row-name"><Monitor size={16} aria-hidden="true"/><span>{item.name}</span>{current === item.id && <small>This computer</small>}</div><div className="download-formats">{item.formats.map(format => latest?.files[item.id]?.[format] ? <a href={latest.files[item.id]![format]} key={format} aria-label={`${item.name} ${format}`}>{format}</a> : null)}</div></div>)}</div></div></section><RemoteCLI/></main>
    <SiteFooter/>
  </>;
}
