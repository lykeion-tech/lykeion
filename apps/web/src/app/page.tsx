'use client';

import { useEffect, useRef, useState } from 'react';
import { ArrowUpRight, X, FileText, ListTodo, Bot, Brain, Monitor, Link2, Maximize2 } from 'lucide-react';
import { loadSession } from '../bridge';
import { typedWord } from '../typing';
import DashboardPreview from './dashboard-preview';
import SiteFooter from './site-footer';
import GetStarted from './get-started';
import { cliIconPaths } from '../../../../packages/cli-icons';

const cases = [
  ['Follow a study on the board', 'See a research question on the board and follow its progress through the stages of a study.', '/docs/studies/', 'Read about studies'],
  ['Review study activity', 'Read the team’s recorded work and findings alongside the research question.', '/docs/runs/', 'Read about runs'],
  ['Explore your agents', 'Browse the agents available in your workspace and see their roles at a glance.', '/docs/agents/', 'Read about agents'],
  ['Bring a Team together', 'Explore the Teams available for the different stages of a study.', '/docs/teams/', 'Read about Teams'],
  ['Meet Aristotle', 'Explore Aristotle’s agent details and the role it plays in your lab.', '/docs/aristotle/', 'Read about Aristotle'],
  ['Explore the lab', 'See the rooms that bring your lab and its work together.', '/docs/workspaces/', 'Read about workspaces'],
];


const directoryFraming = { scale: '1.62', transformOrigin: '51% 19%' };
const photos = [
  ['01-board.webp', 'Study board showing a demo research question'],
  ['02-study.webp', 'Study activity with the Grounding Team’s research findings', { scale: '1.98', transformOrigin: '57% 25%' }],
  ['03-agents.webp', 'List of agents in the demo workspace', directoryFraming],
  ['04-teams.webp', 'Research teams in the demo workspace', directoryFraming],
  ['05-aristotle.webp', 'Aristotle’s agent detail page', { scale: '1.6', transformOrigin: '54% 20%' }],
  ['06-lab.webp', 'The Lykeion lab and its connected rooms'],
] as const;
const roleVideos = [
  ['v1-evidence.mp4', '01-board.webp', 'A study and its evidence'],
  ['v2-aristotle.mp4', '05-aristotle.webp', 'Aristotle’s agent capabilities'],
  ['v3-aristotle-answer.mp4', '01-board.webp', 'Asking Aristotle about a study'],
];

function VideoPreview({ video }: { video: string[] }) {
  const [playing, setPlaying] = useState(false);
  const dialog = useRef<HTMLDialogElement>(null);
  const player = useRef<HTMLVideoElement>(null);
  const open = () => { setPlaying(true); dialog.current?.showModal(); };
  return <><div className="role-art"><img className="video-preview-image" src={`/demo-photos/${video[1]}`} alt=""/><button className="video-preview-play" onClick={open} aria-label={`See ${video[2]} in action`}><span aria-hidden="true">▶</span> See it in action</button></div><dialog ref={dialog} className="video-modal" aria-label={video[2]} onClose={() => setPlaying(false)} onClick={event => { if (event.target === event.currentTarget) dialog.current?.close(); }}><button className="video-modal-close" aria-label="Close video" onClick={() => dialog.current?.close()}><X size={20}/></button><div className="video-modal-frame"><div className="video-modal-bar"><span className="video-modal-dots" aria-hidden="true">● ● ●</span><div className="video-modal-address"><Link2 size={15} aria-hidden="true"/><span>{video[2]}</span></div><button className="video-modal-fullscreen" aria-label="Play video fullscreen" title="Play video fullscreen" onClick={() => { void player.current?.requestFullscreen?.().catch(() => {}); }}><Maximize2 size={16}/></button></div>{playing && <video ref={player} controls autoPlay muted playsInline preload="metadata" aria-label={video[2]}><source src={`/demo-videos/${video[0]}`} type="video/mp4"/></video>}</div></dialog></>;
}

function HeroPhrase() {
  const [word, setWord] = useState('DISCOVERY.');
  useEffect(() => {
    const motion = window.matchMedia('(prefers-reduced-motion: reduce)');
    let timer: ReturnType<typeof setInterval> | undefined;
    const sync = () => {
      clearInterval(timer);
      if (motion.matches) { setWord('DISCOVERY.'); return; }
      const start = performance.now();
      timer = setInterval(() => setWord(typedWord(performance.now() - start)), 90);
    };
    motion.addEventListener('change', sync); sync();
    return () => { clearInterval(timer); motion.removeEventListener('change', sync); };
  }, []);
  return <span className="hero-accent" aria-label="discovery"><span aria-hidden="true">{word}<span className="type-cursor" /></span></span>;
}

export default function Home() {
  const [active, setActive] = useState(0);
  const [canAnimate, setCanAnimate] = useState(false);
  const [rotationKey, setRotationKey] = useState(0);
  const [casesVisible, setCasesVisible] = useState(false);
  const casesRef = useRef<HTMLElement>(null);
  useEffect(() => {
    const observer = new IntersectionObserver(([entry]) => setCasesVisible(entry.isIntersecting), { threshold: 0.2 });
    if (casesRef.current) observer.observe(casesRef.current);
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    const motion = window.matchMedia('(prefers-reduced-motion: reduce)');
    const sync = () => setCanAnimate(!motion.matches && !document.hidden);
    motion.addEventListener('change', sync);
    document.addEventListener('visibilitychange', sync);
    sync();
    return () => { motion.removeEventListener('change', sync); document.removeEventListener('visibilitychange', sync); };
  }, []);
  useEffect(() => {
    if (!canAnimate || !casesVisible) return;
    const timer = window.setTimeout(() => setActive(i => (i + 1) % cases.length), 10000);
    return () => window.clearTimeout(timer);
  }, [active, canAnimate, casesVisible, rotationKey]);
  const [cta, setCta] = useState('/app/#/signin');
  useEffect(() => {
    let live = true;
    async function session() {
      const { user, destination } = await loadSession();
      if (!live || !user) return;
      setCta(destination ?? '/app/');
    }
    void session(); return () => { live = false; };
  }, []);
  return <><header className="site-header"><div className="nav-inner"><a className="brand" href="#top" aria-label="Lykeion home"><img src="/lykeion.svg" alt=""/></a><div className="nav-actions"><a className="github" href="https://github.com/lykeion-tech/lykeion" aria-label="Lykeion on GitHub"><img src="/github.svg" alt="" width="17" height="17"/> <span>GitHub</span></a><a className="nav-login" href={cta}>Log in</a><a className="pill primary" href={cta}>Get started</a></div></div></header>
    <main id="top"><section className="hero"><div className="wrap"><h1>Research, better together.</h1><p>A workspace where agents carry out scientific work, evidence stays connected, and your lab can focus on <HeroPhrase/></p></div></section>
    <div className="works-with" aria-label="Works with Claude Code and Codex"><span>Works with</span><span className="works-with-provider"><svg viewBox="0 0 24 24" width="18" height="18" fill="#D97757" aria-hidden="true"><path d={cliIconPaths.claude}/></svg>Claude Code</span><span className="works-with-provider"><svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true"><path d={cliIconPaths.codex}/></svg>Codex</span></div>
    <div className="product-landscape"><section className="product-intro wrap"><h2>Lykeion</h2><p className="hero-origin"><strong>Designed</strong> and <strong>Engineered</strong> in 🇩🇰 by 🇮🇹 scholars</p><div className="download-buttons"><a className="pill primary" href="/download/">Download app</a><a className="pill demo-button" href="/contact-sales/">Book a demo</a></div><ul className="product-features"><li><Bot size={18} strokeWidth={1.7} aria-hidden="true"/><div><strong>Agents and Teams</strong><p>Work with one agent or choose a Team with roles for each stage.</p></div></li><li><Monitor size={18} strokeWidth={1.7} aria-hidden="true"/><div><strong>Your machine</strong><p>Run supported agent CLIs in the operating system’s sandbox.</p></div></li><li><FileText size={18} aria-hidden="true"/><div><strong>Evidence with provenance</strong><p>Recorded sources and outputs connect work to evidence.</p></div></li></ul></section>
    <div className="dashboard-landscape"><DashboardPreview/></div></div>
    <section className="intro wrap" id="use-cases"><h2>Your questions. Your evidence.<br/>Your whole lab, working together.</h2><p>Set the direction for a study, then follow the evidence as<br/>agents carry out the work.</p></section>
    <section className="cases wrap" ref={casesRef}><div className="section-kicker"><span>USE CASES</span><span>{active + 1} / {cases.length}</span></div><div className="cases-grid"><div className="case-art"><img src={`/demo-use-cases/${photos[active][0]}`} alt={photos[active][1]} style={photos[active][2]}/></div><div className="accordion">{cases.map((item, i) => <div className="accordion-item" key={item[0]}>{active === i && canAnimate && casesVisible && <div className="case-progress" aria-hidden="true" key={`${i}-${rotationKey}`}/>}<button aria-expanded={active === i} onClick={() => { setActive(i); setRotationKey(k => k + 1); }}><span>{item[0]}</span></button>{active === i && <div><p>{item[1]}</p><a href={item[2]} className="pill primary">{item[3]} <ArrowUpRight size={15}/></a></div>}</div>)}</div></div></section>
    <section className="roles wrap" id="workspace"><div className="center-title"><h2>People and agents work together.<br/>On the same research.</h2><p>Different roles, one shared view of the work.</p></div><div className="role-grid">{[['BOARD', 'Start with a question.', 'Create a study, choose an agent or Team, and follow the work as it moves through the board.', 'board'], ['AGENTS', 'Work in a real environment.', 'Agents run the tools they need on a connected machine, with their work and outputs recorded.', 'study'], ['ARISTOTLE', 'Ask your lab a question.', 'Open Aristotle and ask about the studies and evidence your lab has recorded.', 'evidence']].map((r, i) => <article key={r[0]}><div className="role-label"><span aria-hidden="true">{[<ListTodo key="studies" size={17} strokeWidth={1.7}/>, <Bot key="bot" size={17} strokeWidth={1.7}/>, <Brain key="brain" size={17} strokeWidth={1.7}/>][i]}</span><small>{r[0]}</small></div><VideoPreview video={roleVideos[i]}/><h3>{r[1]}</h3><p>{r[2]}</p></article>)}</div></section>
    <section className="split wrap"><div><h2>Agents that work<br/>where you do.</h2><p>Your machine runs the agent CLIs you already use. Lykeion keeps their work connected to the study.</p><a className="pill primary" href={cta}>Open your lab</a><div className="agent-motion" aria-hidden="true"><div className="agent-motion-top"><span>LYKEION</span><span className="agent-motion-status"><i/> WORKING</span></div><div className="agent-motion-body"><div className="agent-motion-orbit"><div className="agent-motion-core"><Bot size={30} strokeWidth={1.4}/></div><span className="agent-motion-dot dot-one"/><span className="agent-motion-dot dot-two"/><span className="agent-motion-dot dot-three"/><span className="agent-motion-dot dot-four"/></div><div className="agent-motion-steps"><span>GROUNDING</span><span>HYPOTHESES</span><span>EXPERIMENTATION</span><span>VALIDATION</span></div></div><div className="agent-motion-bottom"><span>RESEARCH IN PROGRESS</span><span>✳</span></div></div></div><div className="split-art">
      <blockquote className="aristotle-quote"><p>“He who sees things grow from the beginning will have the best view of them.”</p><footer>Aristotle</footer></blockquote>
    </div></section>
    <GetStarted cta={cta}/>
</main>
    <SiteFooter/></>;
}
