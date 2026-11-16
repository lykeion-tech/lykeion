'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import SiteFooter from '../site-footer';
import RemoteCLI from './remote-cli';
import { ArrowUpRight, Download, Monitor } from 'lucide-react';
import { downloadPlatform, type DownloadPlatform } from './platform';
import './download.css';

const release = 'https://github.com/lykeion-tech/lykeion/releases';
const base = `${release}/download/v0.1.0/`;
const downloads: { id: DownloadPlatform; name: string; files: [string, string][] }[] = [
  { id: 'mac-arm64', name: 'macOS Apple Silicon', files: [['.dmg', 'Lykeion-0.1.0-arm64.dmg'], ['.zip', 'Lykeion-0.1.0-arm64-mac.zip']] },
  { id: 'mac-x64', name: 'macOS Intel', files: [['.dmg', 'Lykeion-0.1.0.dmg'], ['.zip', 'Lykeion-0.1.0-mac.zip']] },
  { id: 'linux-x64', name: 'Linux x64', files: [['.AppImage', 'Lykeion-0.1.0.AppImage'], ['.deb', 'lykeion_0.1.0_amd64.deb']] },
  { id: 'linux-arm64', name: 'Linux ARM64', files: [['.AppImage', 'Lykeion-0.1.0-arm64.AppImage'], ['.deb', 'lykeion_0.1.0_arm64.deb']] },
];

type ClientHints = { platform?: string; getHighEntropyValues?: (hints: string[]) => Promise<{ platform?: string; architecture?: string; bitness?: string }> };

export default function DownloadPage() {
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
    <main className="download-page"><section className="download-hero"><div className="download-wrap download-grid"><div className="download-intro"><span className="download-kicker">DESKTOP APP</span><h1>{recommended ? `Download Lykeion for ${recommended.name}` : 'Download Lykeion'}</h1><p>The desktop app comes with the agent daemon, so Claude Code and Codex on this computer can connect to your lab.</p><a className="pill primary download-main-action" href={recommended ? base + recommended.files[0][1] : '#all-downloads'}><Download size={16} aria-hidden="true"/>{recommended ? `Download for ${recommended.name}` : 'Choose your download'}</a><div className="download-release">v0.1.0 <span>·</span> Released Nov 14, 2026 <span>·</span> <a href={`${release}/tag/v0.1.0`}>What’s new <ArrowUpRight size={12} aria-hidden="true"/></a></div></div><div className="download-list" id="all-downloads"><div className="download-list-head"><strong>All downloads</strong><a href={release}>All releases <ArrowUpRight size={12} aria-hidden="true"/></a></div>{downloads.map(item => <div className={`download-row${current === item.id ? ' current' : ''}`} key={item.id}><div className="download-row-name"><Monitor size={16} aria-hidden="true"/><span>{item.name}</span>{current === item.id && <small>This computer</small>}</div><div className="download-formats">{item.files.map(([format, file]) => <a href={base + file} key={file} aria-label={`${item.name} ${format}`}>{format}</a>)}</div></div>)}</div></div></section><RemoteCLI/></main>
    <SiteFooter/>
  </>;
}
