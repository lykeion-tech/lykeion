'use client';

import { useEffect, useRef, useState } from 'react';

const screens = {
  board: { label: 'Studies board', image: '/demo-photos/01-board.webp' },
  study: { label: 'Study activity', image: '/demo-photos/02-study.webp' },
  myStudies: { label: 'My Studies', image: '/demo-preview/demo-my-studies.webp' },
  workflows: { label: 'Workflows', image: '/demo-preview/demo-workflows.webp' },
  projects: { label: 'Projects', image: '/demo-preview/demo-projects.webp' },
  coresearch: { label: 'Coresearch', image: '/demo-preview/demo-coresearch.webp' },
  autoresearch: { label: 'Autoresearch', image: '/demo-preview/demo-autoresearch.webp' },
  colleagues: { label: 'Colleagues', image: '/demo-preview/demo-colleagues.webp' },
  teams: { label: 'Teams', image: '/demo-photos/04-teams.webp' },
  agents: { label: 'Agents', image: '/demo-preview/demo-agents.webp' },
  aristotle: { label: 'Aristotle', image: '/demo-photos/05-aristotle.webp' },
  capabilities: { label: 'Aristotle’s capabilities', image: '/demo-preview/aristotle-capabilities.webp' },
  lab: { label: 'Lykeion lab', image: '/demo-photos/06-lab.webp' },
} as const;
type Screen = keyof typeof screens;
type Hotspot = { label: string; to: Screen; x: number; y: number; width: number; height: number };

const navigation: Hotspot[] = [
  { label: 'Open Lykeion lab', to: 'lab', x: 10, y: 62, width: 145, height: 32 },
  { label: 'Open My Studies', to: 'myStudies', x: 10, y: 215, width: 240, height: 32 },
  { label: 'Open Workflows', to: 'workflows', x: 10, y: 451, width: 240, height: 32 },
  { label: 'Open Studies board', to: 'board', x: 10, y: 284, width: 240, height: 32 },
  { label: 'Open Projects', to: 'projects', x: 10, y: 316, width: 240, height: 32 },
  { label: 'Open Colleagues', to: 'colleagues', x: 10, y: 348, width: 240, height: 32 },
  { label: 'Open Autoresearch', to: 'autoresearch', x: 10, y: 183, width: 240, height: 32 },
  { label: 'Open Teams', to: 'teams', x: 10, y: 419, width: 240, height: 32 },
  { label: 'Open Agents', to: 'agents', x: 10, y: 483, width: 240, height: 32 },
  { label: 'Open Coresearch', to: 'coresearch', x: 186, y: 62, width: 32, height: 28 },
];
const details: Partial<Record<Screen, Hotspot[]>> = {
  study: [{ label: 'Back to Studies', to: 'board', x: 270, y: 65, width: 45, height: 40 }],
  agents: [{ label: 'Open Aristotle', to: 'aristotle', x: 280, y: 807, width: 850, height: 55 }],
  aristotle: [
    { label: 'Open Aristotle’s capabilities', to: 'capabilities', x: 714, y: 126, width: 100, height: 43 },
    { label: 'Back to Agents', to: 'agents', x: 276, y: 68, width: 75, height: 35 },
  ],
  capabilities: [
    { label: 'Open Aristotle’s overview', to: 'aristotle', x: 625, y: 126, width: 90, height: 43 },
    { label: 'Back to Agents', to: 'agents', x: 276, y: 68, width: 75, height: 35 },
  ],
};
const signInActions = [
  { label: 'Create a new item', x: 10, y: 137, width: 240, height: 32 },
  { label: 'Open account', x: 10, y: 850, width: 220, height: 45 },
];

export default function DashboardPreview() {
  const [screen, setScreen] = useState<Screen>('board');
  const viewport = useRef<HTMLDivElement>(null);
  const board = useRef<HTMLDivElement>(null);
  const notice = useRef<HTMLDialogElement>(null);
  const [aristotleOpen, setAristotleOpen] = useState(false);
  const [aristotleExpanded, setAristotleExpanded] = useState(false);
  const requireSignIn = () => notice.current?.showModal();
  useEffect(() => {
    const element = board.current;
    if (!element) return;
    const onWheel = (event: WheelEvent) => {
      if (Math.abs(event.deltaX) > Math.abs(event.deltaY) || event.ctrlKey) return;
      const atStart = element.scrollLeft <= 0;
      const atEnd = element.scrollLeft >= element.scrollWidth - element.clientWidth - 1;
      if ((event.deltaY < 0 && atStart) || (event.deltaY > 0 && atEnd)) return;
      event.preventDefault();
      element.scrollLeft += event.deltaY;
    };
    element.addEventListener('wheel', onWheel, { passive: false });
    return () => element.removeEventListener('wheel', onWheel);
  }, [screen]);
  const show = (next: Screen) => {
    setScreen(next);
    viewport.current?.scrollTo({ top: 0, left: 0 });
  };
  const nav = screen === 'coresearch'
    ? [{ label: 'Back to Studies', to: 'board' as const, x: 10, y: 62, width: 125, height: 32 }]
    : navigation;
  return <section className="dashboard-shell" aria-label="Interactive Lykeion dashboard preview">
    <div className="dashboard-viewport" ref={viewport} tabIndex={0} aria-label="Dashboard preview; scroll to explore">
      <div className="dashboard-screen">
        <img src={screens[screen].image} alt={`Lykeion demo: ${screens[screen].label}`} width="1440" height="900" draggable={false}/>
        {screen === 'board' && <div className="dashboard-board-scroll" ref={board} tabIndex={0} aria-label="Studies board; scroll to see more stages">
          <div className="dashboard-board-content">
            <img src="/demo-preview/board-scroll.webp" alt="Studies board stages" width="2136" height="731" draggable={false}/>
            <button className="dashboard-hotspot board-study" aria-label="Open the sleep and memory study" onClick={() => show('study')}/>
            <button className="dashboard-hotspot board-create" aria-label="Create a study" onClick={requireSignIn}/>
          </div>
        </div>}
        {[...nav, ...(details[screen] ?? [])].map(h => <button key={h.label} className="dashboard-hotspot" aria-label={h.label} title={h.label} onClick={() => show(h.to)} style={{ left: `${h.x / 14.4}%`, top: `${h.y / 9}%`, width: `${h.width / 14.4}%`, height: `${h.height / 9}%` }}/>) }
        {(screen === 'coresearch' ? [] : signInActions).map(h => <button key={h.label} className="dashboard-hotspot" aria-label={h.label} title={h.label} onClick={requireSignIn} style={{ left: `${h.x / 14.4}%`, top: `${h.y / 9}%`, width: `${h.width / 14.4}%`, height: `${h.height / 9}%` }}/>) }
        {!aristotleOpen && <button className="dashboard-hotspot aristotle-trigger" aria-label="Open Aristotle’s floating window" title="Open Aristotle" onClick={() => { setAristotleExpanded(false); setAristotleOpen(true); }} style={{right:'1%',bottom:'1%',width:'4%',height:'6%',zIndex:1,background:screen === 'board' ? 'url(/demo-preview/aristotle-button.webp) center/128% 128% no-repeat' : undefined}}/>}
        {aristotleOpen && <div className={aristotleExpanded ? "dashboard-aristotle expanded" : "dashboard-aristotle"}>
          <img src={aristotleExpanded ? "/demo-preview/aristotle-expanded.webp" : "/demo-preview/aristotle-window.webp"} alt={aristotleExpanded ? "Aristotle’s expanded window" : "Aristotle’s floating window"} width="426" height={aristotleExpanded ? 556 : 116}/>
          <button className="dashboard-hotspot aristotle-close" aria-label="Minimize Aristotle" title="Minimize Aristotle" onClick={() => setAristotleOpen(false)}/>
          <button className="dashboard-hotspot aristotle-chat" aria-label={aristotleExpanded ? "Ask Aristotle" : "Expand Aristotle"} title={aristotleExpanded ? "Ask Aristotle" : "Expand Aristotle"} onClick={aristotleExpanded ? requireSignIn : () => setAristotleExpanded(true)}/>
          {aristotleExpanded && <button className="dashboard-hotspot aristotle-suggestions" aria-label="Ask a suggested question" title="Ask a suggested question" onClick={requireSignIn}/>}
        </div>}
      </div>
    </div>
    <dialog className="dashboard-notice" ref={notice} aria-labelledby="dashboard-notice-title">
      <form method="dialog"><button className="dashboard-notice-close" aria-label="Close">×</button></form>
      <h2 id="dashboard-notice-title">Sign in to continue</h2>
      <p>This is a preview of a sample workspace. Sign in to use this feature in your own lab.</p>
      <a className="pill primary" href="/app/#/signin">Log in</a>
    </dialog>
  </section>;
}
