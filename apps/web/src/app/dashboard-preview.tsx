'use client';

import { useEffect, useRef, useState } from 'react';

const screens = {
  board: { label: 'Studies board', image: '/demo-photos/01-board.webp' },
  study: { label: 'How does tree shade change street temperatures?', image: '/demo-photos/02-study.webp' },
  airQuality: { label: 'Which controls matter in urban air quality comparisons?', image: '/demo-preview/study2.webp' },
  paving: { label: 'Does reflective paving cool nearby sidewalks?', image: '/demo-preview/study3.webp' },
  erosion: { label: 'How sensitive is trail erosion to rainfall intensity?', image: '/demo-preview/study4.webp' },
  replication: { label: 'Does a cooling estimate survive a held-out block?', image: '/demo-preview/study5.webp' },
  myStudies: { label: 'My Studies', image: '/demo-preview/demo-my-studies.webp' },
  workflows: { label: 'Workflows', image: '/demo-preview/demo-workflows.webp' },
  projects: { label: 'Projects', image: '/demo-preview/demo-projects.webp' },
  project: { label: 'Urban Climate Lab', image: '/demo-preview/demo-project.webp' },
  decisions: { label: 'Decisions', image: '/demo-preview/demo-decisions.webp' },
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
  { label: 'Open Lykeion lab', to: 'lab', x: 42, y: 67, width: 68, height: 23 },
  { label: 'Open My Studies', to: 'myStudies', x: 10, y: 215, width: 199, height: 32 },
  { label: 'Open Workflows', to: 'workflows', x: 10, y: 451, width: 199, height: 32 },
  { label: 'Open Studies board', to: 'board', x: 10, y: 284, width: 199, height: 32 },
  { label: 'Open Projects', to: 'projects', x: 10, y: 316, width: 199, height: 32 },
  { label: 'Open Colleagues', to: 'colleagues', x: 10, y: 348, width: 199, height: 32 },
  { label: 'Open Autoresearch', to: 'autoresearch', x: 10, y: 183, width: 199, height: 32 },
  { label: 'Open Teams', to: 'teams', x: 10, y: 419, width: 199, height: 32 },
  { label: 'Open Agents', to: 'agents', x: 10, y: 483, width: 199, height: 32 },
  { label: 'Open Coresearch', to: 'coresearch', x: 146, y: 66, width: 28, height: 24 },
  { label: 'Open Decisions', to: 'decisions', x: 179, y: 66, width: 28, height: 24 },
];
const backToStudies = { label: 'Back to Studies', to: 'board', x: 231, y: 71, width: 20, height: 28 } as const;
const details: Partial<Record<Screen, Hotspot[]>> = {
  study: [backToStudies],
  airQuality: [backToStudies],
  paving: [backToStudies],
  erosion: [backToStudies],
  replication: [backToStudies],
  projects: [{ label: 'Open Urban Climate Lab', to: 'project', x: 239, y: 169, width: 155, height: 24 }],
  project: [{ label: 'Back to Projects', to: 'projects', x: 231, y: 75, width: 20, height: 28 }],
  agents: [{ label: 'Open Aristotle', to: 'aristotle', x: 239, y: 201, width: 1173, height: 53 }],
  aristotle: [
    { label: 'Open Aristotle’s capabilities', to: 'capabilities', x: 683, y: 126, width: 77, height: 43 },
    { label: 'Back to Agents', to: 'agents', x: 235, y: 77, width: 63, height: 32 },
  ],
  capabilities: [
    { label: 'Open Aristotle’s overview', to: 'aristotle', x: 596, y: 126, width: 63, height: 43 },
    { label: 'Back to Agents', to: 'agents', x: 235, y: 77, width: 63, height: 32 },
  ],
};
const signInActions = [
  { label: 'Create a new item', x: 10, y: 138, width: 199, height: 32 },
  { label: 'Open account', x: 10, y: 860, width: 171, height: 32 },
];

// Coordinates are relative to the full board capture, including the offscreen columns.
const boardStudies: { to: Screen; x: number; y: number }[] = [
  { to: 'airQuality', x: 18, y: 56 },
  { to: 'paving', x: 374, y: 56 },
  { to: 'replication', x: 1798, y: 56 },
  { to: 'erosion', x: 1798, y: 261 },
  { to: 'study', x: 1798, y: 466 },
];
const boardWidth = 2136;
const boardHeight = 731;
function position({ x, y, width, height }: Omit<Hotspot, 'label' | 'to'>, imageWidth = 1440, imageHeight = 900) {
  return { left: `${100 * x / imageWidth}%`, top: `${100 * y / imageHeight}%`,
    width: `${100 * width / imageWidth}%`, height: `${100 * height / imageHeight}%` };
}

export default function DashboardPreview() {
  const [trail, setTrail] = useState<Screen[]>(['board']);
  const screen = trail[trail.length - 1];
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
  const resetViewport = () => {
    viewport.current?.scrollTo({ top: 0, left: 0 });
    viewport.current?.focus({ preventScroll: true });
  };
  const show = (next: Screen) => {
    setTrail(current => current[current.length - 1] === next ? current : [...current, next]);
    resetViewport();
  };
  const goBack = () => {
    setTrail(current => current.length > 1 ? current.slice(0, -1) : current);
    resetViewport();
  };
  const isBoard = screen === 'board' || screen === 'myStudies';
  const hasBackArrow = screen === 'coresearch' || screen === 'decisions';
  const nav = hasBackArrow
    ? [{ ...backToStudies, x: 42, y: 67, width: 68, height: 23 }, ...navigation.slice(-2)]
    : navigation;
  return <section className="dashboard-shell" aria-label="Interactive Lykeion dashboard preview">
    <div className="dashboard-viewport" ref={viewport} tabIndex={0} aria-label="Dashboard preview; scroll to explore">
      <div className="dashboard-screen">
        <img src={screens[screen].image} alt={`Lykeion demo: ${screens[screen].label}`} width="1440" height="900" draggable={false}/>
        {isBoard && <div key={screen} className="dashboard-board-scroll" ref={board} tabIndex={0} aria-label="Studies board; scroll to see more stages">
          <div className="dashboard-board-content">
            <img src="/demo-preview/board-scroll.webp" alt="Studies board stages" width={boardWidth} height={boardHeight} draggable={false}/>
            {boardStudies.map(({ to, x, y }) => <button key={to} className="dashboard-hotspot"
              aria-label={`Open study: ${screens[to].label}`} onClick={() => show(to)}
              style={position({ x, y, width: 320, height: 197 }, boardWidth, boardHeight)}/>)}
            {['Grounding', 'Hypotheses', 'Experimentation', 'Validation'].map((stage, index) =>
              <button key={stage} className="dashboard-hotspot" aria-label={`Create a study in ${stage}`} onClick={requireSignIn}
                style={position({ x: 308 + index * 356, y: 16, width: 28, height: 28 }, boardWidth, boardHeight)}/>)}
          </div>
        </div>}
        {hasBackArrow && <button className="dashboard-hotspot" aria-label="Go back" title="Go back" onClick={goBack}
          style={position({ x: 142, y: 22, width: 36, height: 24 })}/>}
        {[...nav, ...(details[screen] ?? [])].map(h => <button key={h.label} className="dashboard-hotspot" aria-label={h.label} title={h.label} onClick={() => show(h.to)} style={position(h)}/>) }
        {signInActions.map(h => <button key={h.label} className="dashboard-hotspot" aria-label={h.label} title={h.label} onClick={requireSignIn} style={position(h)}/>) }
        {!aristotleOpen && <button className="dashboard-hotspot aristotle-trigger" aria-label="Open Aristotle’s floating window" title="Open Aristotle" onClick={() => { setAristotleExpanded(false); setAristotleOpen(true); }} style={{ ...position({ x: 1372, y: 832, width: 48, height: 48 }), zIndex: 1, backgroundImage: isBoard ? 'url(/demo-preview/aristotle-button.webp)' : undefined }}/>}
        {aristotleOpen && <div className={aristotleExpanded ? "dashboard-aristotle expanded" : "dashboard-aristotle"}>
          <img src={aristotleExpanded ? "/demo-preview/aristotle-expanded.webp" : "/demo-preview/aristotle-window.webp"} alt={aristotleExpanded ? "Aristotle’s expanded window" : "Aristotle’s floating window"} width="416" height={aristotleExpanded ? 544 : 104}/>
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
