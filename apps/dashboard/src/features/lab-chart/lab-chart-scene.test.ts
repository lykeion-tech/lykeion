import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { h, nextTick, shallowRef } from 'vue';
import * as THREE from 'three';
import LabChartScene from './LabChartScene.vue';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import type { LabChart, LabChartAgent } from '../../api/types';

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
  measureText(t: string) {
    return { width: t.length * parseFloat(this.font.split(' ')[1]) * 0.5 };
  },
  clearRect() {},
  fillRect() {},
  strokeRect() {},
  beginPath() {},
  arc() {},
  fill() {},
  fillText() {},
};

beforeEach(() => {
  gl.fail = false;
  gl.disposed = 0;
  gl.last = null;
  frames = [];
  Object.assign(globalThis, {
    document: { createElement: () => ({ width: 0, height: 0, getContext: () => ctx }) },
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
          agent('searcher', 'member', working(['Sleep and memory consolidation'])),
          agent('verifier', 'member'),
          agent('lit-notes', 'member', { source: 'researcher' }),
          agent('Grounding auditor', 'auditor'),
        ],
      },
      { stage: 'hypotheses', team: 'Hypotheses Team', agents: [agent('theorist', 'member')] },
      {
        stage: 'experimentation',
        team: 'Experimentation Team',
        agents: [
          agent(
            'leader',
            'leader',
            working(['Sleep and memory consolidation', 'Gut microbiome and mood']),
          ),
        ],
      },
      {
        stage: 'validation',
        team: 'Validation Team',
        agents: [
          agent('checker', 'member', {
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

function mount(chart: LabChart | (() => LabChart), props: Record<string, unknown> = {}) {
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
      clientWidth: 1000,
      clientHeight: 600,
      setPointerCapture() {},
      getBoundingClientRect: () => ({ left: 0, top: 0, width: 1000, height: 600 }),
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
  return { root, app, events, scene, meshes, click, byClass, canvas };
}

it('writes exactly the stage, Team and Conference room names, on upright surfaces only', () => {
  const v = mount(fixture());
  v.scene().updateMatrixWorld(true);
  const texts = v.meshes().filter((m) => typeof m.userData.text === 'string');
  expect(texts.map((m) => m.userData.text).sort()).toEqual(
    [
      'Grounding',
      'Hypotheses',
      'Experimentation',
      'Validation',
      'Grounding Team',
      'Hypotheses Team',
      'Experimentation Team',
      'Validation Team',
      'Conference room',
    ].sort(),
  );
  for (const m of texts) {
    const normal = new THREE.Vector3(0, 0, 1).transformDirection(m.matrixWorld);
    expect(Math.abs(normal.y), m.userData.text).toBeLessThan(1e-9);
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
