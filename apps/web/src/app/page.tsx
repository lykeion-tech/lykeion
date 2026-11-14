'use client';

import { useEffect, useRef, useState } from 'react';
import { ArrowUpRight, ArrowRight, Menu, X, FileText, ListTodo, Bot, Brain, Monitor, Link2, Maximize2 } from 'lucide-react';
import { loadSession } from '../bridge';
import { typedWord } from '../typing';
import DashboardPreview from './dashboard-preview';

const cases = [
  ['Follow a study on the board', 'See a research question on the board and follow its progress through the stages of a study.', 'board'],
  ['Review study activity', 'Read the team’s recorded work and findings alongside the research question.', 'evidence'],
  ['Explore your agents', 'Browse the agents available in your workspace and see their roles at a glance.', 'agents'],
  ['Bring a Team together', 'Explore the Teams available for the different stages of a study.', 'teams'],
  ['Meet Aristotle', 'Explore Aristotle’s agent details and the role it plays in your lab.', 'aristotle'],
  ['Explore the lab', 'See the rooms that bring your lab and its work together.', 'lab'],
];


const photos = [
  ['01-board.webp', 'Study board showing a demo research question', 'Study board'],
  ['02-study.webp', 'Study activity with the Grounding Team’s research findings', 'Study activity'],
  ['03-agents.webp', 'List of agents in the demo workspace', 'Agents'],
  ['04-teams.webp', 'Research teams in the demo workspace', 'Teams'],
  ['05-aristotle.webp', 'Aristotle’s agent detail page', 'Aristotle'],
  ['06-lab.webp', 'The Lykeion lab and its connected rooms', 'The lab'],
];
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
  const [word, setWord] = useState('DISCOVERY');
  useEffect(() => {
    const motion = window.matchMedia('(prefers-reduced-motion: reduce)');
    let timer: ReturnType<typeof setInterval> | undefined;
    const sync = () => {
      clearInterval(timer);
      if (motion.matches) { setWord('DISCOVERY'); return; }
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
  const [menu, setMenu] = useState(false);
  const [cta, setCta] = useState('/app/#/signin');
  useEffect(() => {
    let live = true;
    async function session() {
      const { user, destination } = await loadSession();
      if (!live || !user) return;
      setCta(destination ?? '/app/');
      if (destination) window.location.replace(destination);
    }
    void session(); return () => { live = false; };
  }, []);
  return <><header className="site-header"><div className="nav-inner"><a className="brand" href="#top"><img src="/lykeion.svg" alt=""/><span className="brand-name">Lykeion</span></a><nav className={menu ? 'nav-links open' : 'nav-links'} aria-label="Main navigation"><a href="#use-cases" onClick={() => setMenu(false)}>Platform</a><a href="#workspace" onClick={() => setMenu(false)}>Workspace</a><a href="#features" onClick={() => setMenu(false)}>Features</a></nav><div className="nav-actions"><a className="github" href="https://github.com/lykeion-tech/lykeion" aria-label="Lykeion on GitHub"><img src="/github.svg" alt="" width="17" height="17"/> <span>GitHub</span></a><a className="nav-login" href={cta}>Log in</a><a className="pill primary" href={cta}>Get started</a><button className="mobile-menu" aria-label={menu ? 'Close menu' : 'Open menu'} aria-expanded={menu} onClick={() => setMenu(!menu)}>{menu ? <X/> : <Menu/>}</button></div></div></header>
    <main id="top"><section className="hero wrap"><h1>Research, better together.</h1><p>A workspace where agents carry out scientific work, evidence stays connected, and your lab can focus on <HeroPhrase/></p><div className="hero-actions"><div className="hero-downloads"><div className="download-buttons"><button className="pill primary" disabled title="Coming soon">Download for macOS</button><button className="pill demo-button" disabled title="Coming soon">Book a demo</button></div><p className="download-platforms">Also for Intel · Linux</p></div><p className="hero-origin"><mark>Designed</mark> and <mark>Engineered</mark><span className="hero-origin-place">in 🇩🇰 by an 🇮🇹</span></p></div></section>
    <div className="dashboard-landscape"><DashboardPreview/></div>
    <section className="intro wrap" id="use-cases"><h2>Your question. Your evidence.<br/>Your whole lab, working together.</h2><p>Turn a research question into a study that agents can work<br/>and people can steer.</p></section>
    <section className="cases wrap" ref={casesRef}><div className="section-kicker">USE CASES <span>{active + 1} / {cases.length}</span></div><div className="cases-grid"><div className="case-art"><img src={`/demo-use-cases/${photos[active][0]}`} alt={photos[active][1]}/></div><div className="accordion">{cases.map((item, i) => <div className="accordion-item" key={item[0]}>{active === i && canAnimate && casesVisible && <div className="case-progress" aria-hidden="true" key={`${i}-${rotationKey}`}/>}<button aria-expanded={active === i} onClick={() => { setActive(i); setRotationKey(k => k + 1); }}><span>{item[0]}</span><span>{active === i ? '−' : '+'}</span></button>{active === i && <div><p>{item[1]}</p><a href="#workspace" className="pill primary">Explore the platform <ArrowUpRight size={15}/></a></div>}</div>)}</div></div></section>
    <section className="roles wrap" id="workspace"><div className="center-title"><h2>People and agents work together.<br/>On the same research.</h2><p>Different roles, one shared view of the work.</p></div><div className="role-grid">{[['BOARD', 'Start with a question.', 'Create a study, choose an agent or Team, and follow the work as it moves through the board.', 'board'], ['AGENT', 'Work in a real environment.', 'Agents run the tools they need on a connected machine, with their work and outputs recorded.', 'study'], ['ARISTOTLE', 'Ask your lab a question.', 'Open Aristotle and ask about the studies and evidence your lab has recorded.', 'evidence']].map((r, i) => <article key={r[0]}><div className="role-label"><span aria-hidden="true">{[<ListTodo key="studies" size={17} strokeWidth={1.7}/>, <Bot key="bot" size={17} strokeWidth={1.7}/>, <Brain key="brain" size={17} strokeWidth={1.7}/>][i]}</span><small>{r[0]}</small></div><VideoPreview video={roleVideos[i]}/><h3>{r[1]}</h3><p>{r[2]}</p></article>)}</div></section>
    <section className="split wrap"><div><h2>Agents that work<br/>where you do.</h2><p>Your machine runs the agent CLIs you already use. Lykeion keeps their work connected to the study.</p><a className="pill primary" href={cta}>Open your lab</a><ul><li><Bot size={18} strokeWidth={1.7} aria-hidden="true"/><div><strong>Agents and Teams</strong><p>Work with one agent or choose a Team with roles for each stage.</p></div></li><li><Monitor size={18} strokeWidth={1.7} aria-hidden="true"/><div><strong>Your machine</strong><p>Run supported agent CLIs in the operating system’s sandbox.</p></div></li><li><FileText size={18}/><div><strong>Evidence with provenance</strong><p>Recorded sources and outputs connect work to evidence.</p></div></li></ul></div><div className="split-art" aria-hidden="true">
      <svg className="science-motion" aria-hidden="true" viewBox="0 0 600 620" xmlns="http://www.w3.org/2000/svg">
        <defs><linearGradient id="science-path" x1="0" x2="1" y1="0" y2="1"><stop stopColor="#006dd5"/><stop offset="1" stopColor="#e6b62c"/></linearGradient></defs>
        <circle className="science-orbit orbit-one" cx="300" cy="300" r="208"/><circle className="science-orbit orbit-two" cx="300" cy="300" r="150"/>
        <path className="science-link" d="M145 188 L300 300 L455 180 M138 435 L300 300 L460 440"/>
        <path className="science-flow" d="M145 188 L300 300 L455 180 M138 435 L300 300 L460 440"/>
        <g className="science-center"><rect x="211" y="244" width="178" height="112" rx="13"/><circle cx="238" cy="272" r="7"/><path d="M230 297h139 M230 315h112 M230 333h81"/><text x="255" y="275">SHARED STUDY</text></g>
        <g className="science-node node-a"><circle cx="145" cy="188" r="36"/><path d="M145 170a9 9 0 1 0 0 18 9 9 0 0 0 0-18m-16 36c1-10 7-15 16-15s15 5 16 15"/><text x="145" y="250">RESEARCHER</text></g>
        <g className="science-node node-b"><circle cx="455" cy="180" r="36"/><rect x="439" y="172" width="32" height="25" rx="6"/><path d="M455 165v7m-8 10h1m14 0h1m-15 8h14"/><text x="455" y="242">AGENT</text></g>
        <g className="science-node node-c"><circle cx="138" cy="435" r="36"/><path d="M128 419h20l7 7v25h-27zm19 0v9h8m-20 8h13m-13 7h13"/><text x="138" y="497">EVIDENCE</text></g>
        <g className="science-node node-d"><circle cx="460" cy="440" r="36"/><path d="M445 452v-11h7v11m4 0v-20h7v20m4 0v-27h7v27"/><text x="460" y="502">FINDINGS</text></g>
        <circle className="science-pulse pulse-one" r="4"><animateMotion dur="5s" repeatCount="indefinite" path="M145 188 L300 300 L455 180"/></circle><circle className="science-pulse pulse-two" r="4"><animateMotion dur="6s" repeatCount="indefinite" path="M460 440 L300 300 L138 435"/></circle>
      </svg>
    </div></section>
    <section className="features wrap" id="features"><div className="center-title"><h2>A workspace built for<br/>the whole research process.</h2><p>The tools around the board, together in one place.</p></div></section>
    <section className="get-started wrap"><div><small>GET STARTED</small><h2>Give your next question<br/>to the lab.</h2><p>Open a workspace, connect a machine, and start a study.</p><a className="pill primary" href={cta}>Open your lab</a><a className="subtle-link" href="#use-cases">Explore Lykeion <ArrowRight size={16}/></a></div><div className="cta-art"><div className="download-buttons"><button className="pill primary" disabled title="Coming soon">Download for macOS</button><button className="pill demo-button" disabled title="Coming soon">Book a demo</button></div></div></section>
    <section className="resources wrap"><a href="#use-cases"><small>THE BOARD</small><h3>From a question to a study</h3><p>Follow the work across the board, with its evidence and outputs close at hand.</p><span>Explore the board <ArrowUpRight size={15}/></span></a><div><a href="#workspace"><small>WORKSPACE</small><h3>Around the board</h3><p>Meet the agents, Teams and tools that make up a Lykeion lab.</p></a><a href="#features"><small>CORESEARCH</small><h3>Work alongside your agents</h3><p>Experiment with files and runs, or write together in Cowrite.</p></a></div></section></main>
    <footer className="footer">
      <div className="footer-main wrap">
        <div className="footer-intro"><strong>Lykeion</strong><p>A research lab where agents work<br/>and people steer.</p></div>
        <nav className="footer-columns" aria-label="Footer navigation">
          <div><h2>Compare</h2><span aria-disabled="true">vs. Claude Science</span><span aria-disabled="true">vs. Open Science</span><span aria-disabled="true">vs. K-Dense BYOK</span><span aria-disabled="true">vs. Elicit</span></div>
          <div><h2>Community</h2><span aria-disabled="true">Forum</span><span aria-disabled="true">Discord</span></div>
          <div><h2>Developers</h2><span aria-disabled="true">Documentation</span><a href="https://github.com/lykeion-tech/lykeion">GitHub</a><a href="https://github.com/lykeion-tech/lykeion/contribute">Contributing</a></div>
          <div><h2>Company</h2><span aria-disabled="true">About</span><span aria-disabled="true">Contact</span></div>
        </nav>
      </div>
      <div className="footer-bottom"><div className="wrap footer-bottom-inner"><div className="footer-copyright"><img src="/lykeion.svg" alt=""/><span aria-hidden="true">{'//'}</span><p>Copyright © 2026 Lykeion.<br/>All rights reserved.</p></div><div className="footer-bottom-links"><div><a href="/privacy/">Privacy</a><a href="/terms/">Terms</a></div><a href="https://github.com/lykeion-tech/lykeion" aria-label="Lykeion on GitHub"><img src="/github.svg" width="15" height="15" alt=""/></a></div></div></div>
    </footer></>;
}
