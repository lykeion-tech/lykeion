import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { h, nextTick, shallowRef } from 'vue';
import * as THREE from 'three';
import LabChartScene from './LabChartScene.vue';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import type { LabChart, LabChartAgent, LabChartRoom } from '../../api/types';

const gl = vi.hoisted(() => ({
  fail: false,
  disposed: 0,
  last: null as null | { scene: unknown; camera: unknown },
}));

vi.mock('three', async (importOriginal) => {
  const real = await importOriginal<typeof import('three')>();
  class WebGLRenderer {
    shadowMap = { enabled: false, type: 0 };
    outputColorSpace = '';
    constructor() {
      if (gl.fail) throw new Error('Error creating WebGL context.');
    }
    setPixelRatio() {}
    setSize() {}
    render(scene: unknown, camera: unknown) {
      gl.last = { scene, camera };
    }
    dispose() {
      gl.disposed++;
    }
  }
  return { ...real, WebGLRenderer };
});

let frames: FrameRequestCallback[] = [];
const flush = (n = 3) => {
  for (let i = 0; i < n; i++) {
    const run = frames;
    frames = [];
    run.forEach((cb) => cb(0));
  }
};

const ctx = {
  font: '',
  // Average advance per character as Chromium measures these faces: about 0.5 em for Barlow
  // and Caveat, 0.41 em for Barlow Condensed.
  measureText(t: string) {
    const em = this.font.includes('Condensed') ? 0.41 : 0.5;
    return { width: t.length * parseFloat(this.font.split(' ')[1]) * em };
  },
  clearRect() {},
  fillRect() {},
  strokeRect() {},
  beginPath() {},
  arc() {},
  fill() {},
  fillText(this: { canvas: Canvas; font: string }, text: string, x: number, y: number) {
    this.canvas.drawn.push({ text, x, y, font: this.font, align: (this as never)['textAlign'] });
  },
  moveTo() {},
  lineTo() {},
  quadraticCurveTo() {},
  bezierCurveTo() {},
  closePath() {},
  stroke() {},
  ellipse() {},
  save() {},
  restore() {},
  translate() {},
  rotate() {},
  setLineDash() {},
};
type Canvas = {
  width: number;
  height: number;
  drawn: { text: string; x: number; y: number; font: string; align: string }[];
  getContext(): unknown;
};
let canvases: Canvas[] = [];

beforeEach(() => {
  gl.fail = false;
  gl.disposed = 0;
  gl.last = null;
  frames = [];
  canvases = [];
  Object.assign(globalThis, {
    document: {
      createElement: () => {
        const cv: Canvas = { width: 0, height: 0, drawn: [], getContext: () => g };
        const g = Object.assign(Object.create(ctx), { canvas: cv });
        canvases.push(cv);
        return cv;
      },
    },
    requestAnimationFrame: (cb: FrameRequestCallback) => frames.push(cb),
    cancelAnimationFrame: () => {},
  });
});
afterEach(() => vi.restoreAllMocks());

let n = 0;
const agent = (
  name: string,
  role: LabChartAgent['role'],
  extra: Partial<LabChartAgent> = {},
): LabChartAgent => ({
  id: `a${++n}`,
  name,
  emoji: '',
  role,
  source: 'laboratory',
  status: 'idle',
  since: null,
  studies: [],
  machine: null,
  cli: null,
  reports_to: role === 'leader' ? 'the researchers' : 'Grounding leader',
  ...extra,
});

function fixture(): LabChart {
  const working = (titles: string[]) => ({
    status: 'working' as const,
    since: new Date(Date.now() - 4 * 60_000).toISOString(),
    studies: titles.map((title, i) => ({ id: `s${i}`, title })),
    machine: 'Lab Mac',
    cli: 'claude' as const,
  });
  return {
    rooms: [
      {
        stage: 'grounding',
        team: 'Grounding Team',
        agents: [
          agent('Grounding leader', 'leader'),
          agent('searcher · Grounding Team', 'member', working(['Sleep and memory consolidation'])),
          agent('verifier', 'member'),
          agent('lit-notes · Grounding Team', 'member', {
            ...working(['Sleep and memory consolidation']),
            source: 'researcher',
          }),
          agent('Grounding auditor', 'auditor'),
        ],
      },
      { stage: 'hypotheses', team: 'Hypotheses Team', agents: [agent('theorist', 'member')] },
      {
        stage: 'experimentation',
        team: 'Experimentation Team',
        agents: [
          agent(
            'leader · Experimentation Team',
            'leader',
            working(['Sleep and memory consolidation', 'Gut microbiome and mood']),
          ),
        ],
      },
      {
        stage: 'validation',
        team: 'Validation Team',
        agents: [
          agent('checker · Validation Team', 'member', {
            status: 'needs_you',
            since: new Date().toISOString(),
            studies: [{ id: 's9', title: 'Caffeine and reaction time' }],
          }),
        ],
      },
    ],
    people: [
      { user_id: 'u1', name: 'you', role: 'owner', open_decisions: 1 },
      { user_id: 'u2', name: 'colleague', role: 'member', open_decisions: 0 },
    ],
    ticker: {
      at: new Date().toISOString(),
      text: 'checker is waiting for your answer on “Caffeine and reaction time”',
    },
  };
}

const hasClass = (x: { props: Record<string, unknown> }, c: string) =>
  String(x.props.class ?? '')
    .split(/\s+/)
    .includes(c);

// The built-in laboratory: four Teams of 18 agents, two people.
function builtIn(): LabChart {
  const teams: [LabChartRoom['stage'], string, string[]][] = [
    ['grounding', 'Grounding Team', ['searcher', 'verifier']],
    ['hypotheses', 'Hypotheses Team', ['scout', 'theorist']],
    ['experimentation', 'Experimentation Team', ['analyst', 'data-scout', 'planner']],
    ['validation', 'Validation Team', ['checker', 'literature-checker', 'reproducer']],
  ];
  return {
    rooms: teams.map(([stage, team, members]) => ({
      stage,
      team,
      agents: [
        agent(`leader · ${team}`, 'leader'),
        ...members.map((m) => agent(`${m} · ${team}`, 'member')),
        agent(`auditor · ${team}`, 'auditor'),
      ],
    })),
    people: [
      { user_id: 'u1', name: 'you', role: 'owner', open_decisions: 0 },
      { user_id: 'u2', name: 'colleague', role: 'member', open_decisions: 0 },
    ],
    ticker: null,
  };
}

function mount(
  chart: LabChart | (() => LabChart),
  props: Record<string, unknown> = {},
  size = { w: 1000, h: 600 },
) {
  const root = node('root');
  const events: [string, string][] = [];
  const app = createTreeApp({
    render: () =>
      h(LabChartScene, {
        chart: typeof chart === 'function' ? chart() : chart,
        me: 'u1',
        ...props,
        onOpenAgent: (id: string) => events.push(['agent', id]),
        onOpenPerson: (id: string) => events.push(['person', id]),
      }),
  });
  app.mount(root);
  const canvas = all(root).find((x) => x.tag === 'canvas');
  if (canvas)
    Object.assign(canvas, {
      clientWidth: size.w,
      clientHeight: size.h,
      setPointerCapture() {},
      getBoundingClientRect: () => ({ left: 0, top: 0, width: size.w, height: size.h }),
    });
  flush();
  const scene = () => gl.last!.scene as THREE.Scene;
  const camera = () => gl.last!.camera as THREE.OrthographicCamera;
  const meshes = () => {
    const out: THREE.Mesh[] = [];
    scene().traverse((o) => o instanceof THREE.Mesh && out.push(o));
    return out;
  };
  const click = (key: string) => {
    scene().updateMatrixWorld(true);
    const box = meshes().find((m) => m.userData.key === key)!;
    const p = box.getWorldPosition(new THREE.Vector3()).project(camera());
    const e = {
      clientX: ((p.x + 1) / 2) * 1000,
      clientY: ((1 - p.y) / 2) * 600,
      pointerId: 1,
      currentTarget: canvas,
      preventDefault() {},
    };
    (canvas!.props.onPointerdown as (e: unknown) => void)(e);
    (canvas!.props.onPointerup as (e: unknown) => void)(e);
  };
  const byClass = (c: string) => all(root).filter((x) => hasClass(x, c));
  return { root, app, events, scene, camera, meshes, click, byClass, canvas };
}

const STAGE_NAMES = ['Grounding', 'Hypotheses', 'Experimentation', 'Validation'];
const TEAM_NAMES = STAGE_NAMES.map((n) => `${n} Team`);

it('writes only the Team and Conference room names, on upright surfaces only', () => {
  const v = mount(fixture());
  v.scene().updateMatrixWorld(true);
  const texts = v.meshes().filter((m) => typeof m.userData.text === 'string');
  expect(texts.map((m) => m.userData.text).sort()).toEqual([...TEAM_NAMES, 'Conference room'].sort());
  for (const m of texts) expect(STAGE_NAMES).not.toContain(m.userData.text);
  for (const m of texts) expect(m.userData.lines).toHaveLength(2);
  for (const m of [...texts, ...v.meshes().filter((m) => m.userData.board)]) {
    const normal = new THREE.Vector3(0, 0, 1).transformDirection(m.matrixWorld);
    expect(Math.abs(normal.y), String(m.userData.text ?? m.userData.board)).toBeLessThan(1e-9);
  }
  v.app.unmount();
});

it('covers each chalkboard in its own scientific writing, with no stage or Team name', () => {
  const v = mount(fixture());
  const boards = v.meshes().filter((m) => m.userData.board);
  expect(boards.map((m) => m.userData.board).sort()).toEqual(
    ['experimentation', 'grounding', 'hypotheses', 'validation'],
  );
  const writing = boards.map((m) => (m.userData.writing as string[]).join('\n'));
  expect(new Set(writing).size).toBe(4);
  for (const w of writing) {
    expect(w.length).toBeGreaterThan(20);
    for (const name of [...STAGE_NAMES, ...TEAM_NAMES]) expect(w).not.toContain(name);
  }
  v.app.unmount();
});

it('keeps every chalk letter inside its board', () => {
  const v = mount(fixture());
  const boards = canvases.filter((c) => c.drawn.some((d) => d.font.includes('Caveat')));
  expect(boards).toHaveLength(4);
  for (const cv of boards)
    for (const d of cv.drawn) {
      const size = parseFloat(d.font.split(' ')[1]);
      const w = d.text.length * size * 0.5;
      const x0 = d.align === 'center' ? d.x - w / 2 : d.x;
      expect(x0, d.text).toBeGreaterThanOrEqual(0);
      expect(x0 + w, d.text).toBeLessThanOrEqual(cv.width);
      expect(d.y - size / 2, d.text).toBeGreaterThanOrEqual(0);
      expect(d.y + size / 2, d.text).toBeLessThanOrEqual(cv.height);
    }
  v.app.unmount();
});

it('shows a pill only for working agents and agents that need an answer', () => {
  const v = mount(fixture());
  const pills = v.byClass('lab-chart-pill');
  expect(pills.map((p) => textOf(p).trim()).sort()).toEqual(
    [
      'checker · needs your answer',
      'leader · 2 studies',
      'searcher · Sleep and memory consolidation',
      'lit-notes · Grounding Team · Sleep and memory consolidation',
    ].sort(),
  );
  const ask = pills.find((p) => textOf(p).includes('checker'))!;
  expect(hasClass(ask, 'ask')).toBe(true);
  expect(pills.filter((p) => hasClass(p, 'ask'))).toHaveLength(1);
  v.app.unmount();
});

it('opens the agent panel from its hitbox and links to the agent', async () => {
  const chart = fixture();
  const searcher = chart.rooms[0].agents[1];
  const v = mount(chart);
  v.click(`a:${searcher.id}`);
  await nextTick();
  expect(textOf(v.byClass('lab-chart-panel-name')[0])).toBe('searcher');
  const rows = v.byClass('lab-chart-panel-row').map((r) => textOf(r.children[0]));
  expect(rows).toEqual(['Room', 'Reports to', 'Status', 'Studies', 'Machine', 'Made by']);
  expect(textOf(v.byClass('lab-chart-panel-row')[0].children[1])).toBe('Grounding');
  expect(textOf(v.byClass('lab-chart-panel-row')[2].children[1])).toBe('Working · 4 min');
  expect(textOf(v.byClass('lab-chart-panel-row')[4].children[1])).toBe('Lab Mac · Claude Code');
  const link = all(v.root).find((x) => x.tag === 'button' && textOf(x) === 'Open agent →')!;
  (link.props.onClick as () => void)();
  expect(v.events).toEqual([['agent', searcher.id]]);
  v.app.unmount();
});

it('keeps the preview quiet: no pills, no ticker, a panel with name, role and room', async () => {
  const chart = fixture();
  const v = mount(chart, { preview: true });
  expect(v.byClass('lab-chart-pill')).toHaveLength(0);
  expect(v.byClass('lab-chart-tick')).toHaveLength(0);
  expect(
    v
      .meshes()
      .filter((m) => String(m.userData.key).startsWith('p:'))
      .map((m) => m.userData.key),
  ).toEqual(['p:u1']);
  v.click(`a:${chart.rooms[0].agents[1].id}`);
  await nextTick();
  expect(textOf(v.byClass('lab-chart-panel-name')[0])).toBe('searcher');
  expect(textOf(v.byClass('lab-chart-panel-role')[0])).toBe('member');
  expect(v.byClass('lab-chart-panel-row').map((r) => textOf(r.children[0]))).toEqual(['Room']);
  expect(all(v.root).some((x) => x.tag === 'button' && textOf(x).includes('→'))).toBe(false);
  v.app.unmount();
});

it('shows the ticker on the page', () => {
  const v = mount(fixture());
  expect(textOf(v.byClass('lab-chart-tick')[0])).toContain('checker is waiting for your answer');
  v.app.unmount();
});

it('falls back to the list when WebGL is unavailable', async () => {
  gl.fail = true;
  const chart = fixture();
  const v = mount(chart);
  await nextTick();
  expect(all(v.root).some((x) => x.tag === 'canvas')).toBe(false);
  const list = v.byClass('lab-chart-list')[0];
  expect(hasClass(list, 'is-hidden')).toBe(false);
  const rooms = v.byClass('lab-chart-list-room');
  const status = { idle: 'Idle', working: 'Working', needs_you: 'Needs you' };
  for (const room of chart.rooms) {
    const block = rooms.find(
      (r) => textOf(r.children[0]) === room.stage[0].toUpperCase() + room.stage.slice(1),
    )!;
    const items = all(block)
      .filter((x) => x.tag === 'li')
      .map(textOf);
    expect(items).toEqual(room.agents.map((a) => `${a.name} · ${a.role} · ${status[a.status]}`));
  }
  v.app.unmount();
});

it('hides the list visually while the scene draws', () => {
  const v = mount(fixture());
  expect(hasClass(v.byClass('lab-chart-list')[0], 'is-hidden')).toBe(true);
  v.app.unmount();
});

it('has the exact hint and no Orbit or Night control', () => {
  const v = mount(fixture());
  expect(textOf(v.byClass('lab-chart-hint')[0])).toBe(
    'drag to look around · wheel to zoom · click an agent or a person for details',
  );
  expect(textOf(v.root)).not.toMatch(/Orbit|Night/);
  v.app.unmount();
});

it('rebuilds the rooms when the chart changes and disposes everything on unmount', async () => {
  const chart = shallowRef(fixture());
  const v = mount(() => chart.value);
  const boxes = () => v.meshes().filter((m) => m.userData.key).length;
  const before = boxes();
  const scene = v.scene();
  flush(5);
  expect(boxes()).toBe(before);
  const geometryDispose = vi.spyOn(THREE.BufferGeometry.prototype, 'dispose');
  const next = fixture();
  next.rooms[1].agents.push(agent('scout', 'member'));
  chart.value = next;
  await nextTick();
  flush();
  expect(v.scene()).toBe(scene);
  expect(boxes()).toBe(before + 1);
  expect(geometryDispose).toHaveBeenCalled();
  const textureDispose = vi.spyOn(THREE.Texture.prototype, 'dispose');
  v.app.unmount();
  expect(gl.disposed).toBe(1);
  expect(textureDispose).toHaveBeenCalled();
});

it('draws each text texture once, however often the chart changes', async () => {
  const chart = shallowRef(fixture());
  const v = mount(() => chart.value);
  const made = vi.spyOn(THREE, 'CanvasTexture');
  const next = fixture();
  next.rooms[0].agents[2].status = 'working';
  chart.value = next;
  await nextTick();
  flush();
  expect(made).not.toHaveBeenCalled();
  v.scene().updateMatrixWorld(true);
  expect(v.meshes().filter((m) => typeof m.userData.text === 'string')).toHaveLength(5);
  expect(v.meshes().filter((m) => m.userData.board)).toHaveLength(4);
  v.app.unmount();
});

// Mounts the chart on a canvas of the given size and lets the opening ease settle.
function settled(
  chart: LabChart,
  size: { w: number; h: number },
  props: Record<string, unknown> = {},
) {
  vi.useFakeTimers({ toFake: ['setTimeout'] });
  frames = [];
  try {
    const v = mount(chart, props, size);
    vi.advanceTimersByTime(600);
    flush(300);
    v.scene().updateMatrixWorld(true);
    return v;
  } finally {
    vi.useRealTimers();
  }
}
const toScreen = (p: THREE.Vector3, cam: THREE.Camera, size: { w: number; h: number }) => {
  const q = p.clone().project(cam);
  return new THREE.Vector2(((q.x + 1) / 2) * size.w, ((1 - q.y) / 2) * size.h);
};

it('settles with every TV line at least 12px tall on a 1200×830 canvas', () => {
  const size = { w: 1200, h: 830 };
  const v = settled(builtIn(), size);
  const cam = v.camera();
  const texts = v.meshes().filter((m) => typeof m.userData.text === 'string');
  expect(texts).toHaveLength(5);
  for (const m of texts) {
    const centre = m.localToWorld(new THREE.Vector3(0, 0, 0));
    const top = m.localToWorld(new THREE.Vector3(0, m.userData.fontSize as number, 0));
    const line = toScreen(centre, cam, size).distanceTo(toScreen(top, cam, size));
    expect(line, m.userData.text).toBeGreaterThanOrEqual(12);
  }
  v.app.unmount();
});

it('fits the whole plan inside the canvas once settled, in the page and the onboarding preview', () => {
  for (const [size, props] of [
    [{ w: 1200, h: 830 }, {}],
    [{ w: 1000, h: 600 }, {}],
    [{ w: 560, h: 420 }, { preview: true }],
  ] as const) {
    const v = settled(builtIn(), size, props);
    const cam = v.camera();
    const box = new THREE.Box3();
    let x0 = Infinity,
      x1 = -Infinity,
      y0 = Infinity,
      y1 = -Infinity;
    for (const m of v
      .meshes()
      .filter((m) => m.userData.key || m.userData.board || typeof m.userData.text === 'string')) {
      box.setFromObject(m);
      for (const c of [box.min, box.max]) {
        const p = toScreen(c, cam, size);
        x0 = Math.min(x0, p.x);
        x1 = Math.max(x1, p.x);
        y0 = Math.min(y0, p.y);
        y1 = Math.max(y1, p.y);
      }
    }
    const where = `${size.w}×${size.h}: x ${x0.toFixed(0)}–${x1.toFixed(0)}, y ${y0.toFixed(0)}–${y1.toFixed(0)}`;
    expect(x0, where).toBeGreaterThanOrEqual(0);
    expect(y0, where).toBeGreaterThanOrEqual(0);
    expect(x1, where).toBeLessThanOrEqual(size.w);
    expect(y1, where).toBeLessThanOrEqual(size.h);
    // The plan fills the canvas: it spans most of one dimension.
    expect(Math.max((x1 - x0) / size.w, (y1 - y0) / size.h), where).toBeGreaterThan(0.75);
    v.app.unmount();
  }
});

it('hangs every TV between its bookshelf and the windows, clear of both', () => {
  const v = mount(builtIn());
  v.scene().updateMatrixWorld(true);
  const rooms = v.scene().children.find((o) => o instanceof THREE.Group)!;
  const parts = (hex: number) => {
    const out: THREE.Box3[] = [];
    rooms.traverse((o) => {
      const m = o as THREE.Mesh;
      if (m.isMesh && (m.material as THREE.MeshLambertMaterial).color?.getHex() === hex)
        out.push(new THREE.Box3().setFromObject(m));
    });
    return out;
  };
  const tvs = parts(0x1b1c1e);
  const blocked = [...parts(0x8fb8d8), ...parts(0x6b4f35)];
  expect(tvs).toHaveLength(5);
  expect(blocked.length).toBeGreaterThan(5 * 3);
  for (const tv of tvs) {
    expect(tv.max.x - tv.min.x).toBeLessThanOrEqual(8.2 + 1e-9);
    expect(tv.max.y).toBeLessThanOrEqual(4.8);
    for (const b of blocked) expect(tv.intersectsBox(b), JSON.stringify([tv, b])).toBe(false);
  }
  v.app.unmount();
});
