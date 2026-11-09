<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, shallowRef, watch } from 'vue';
import * as THREE from 'three';
import type { LabChart, LabChartAgent, LabChartPerson, LabChartRoom } from '../../api/types';
import LabChartList from './LabChartList.vue';
import {
  addSurroundings,
  AUDITOR,
  buildRooms,
  COL,
  disposeObject,
  fitView,
  hex,
  STAGE_LABEL,
  type Rooms,
} from './scene';

const props = withDefaults(
  defineProps<{ chart: LabChart; preview?: boolean; height?: string; me?: string }>(),
  { preview: false, height: '100%', me: '' },
);
const emit = defineEmits<{ 'open-agent': [id: string]; 'open-person': [userId: string] }>();

type Entity =
  | {
      key: string;
      kind: 'agent';
      agent: LabChartAgent;
      stage: LabChartRoom['stage'];
      name: string;
    }
  | { key: string; kind: 'person'; person: LabChartPerson };

// The preview's Conference room seats only the person looking at it.
const shown = computed<LabChart>(() => ({
  ...props.chart,
  people:
    props.preview && props.me
      ? props.chart.people.filter((p) => p.user_id === props.me)
      : props.chart.people,
}));
// Laboratory agents are named "<member> · <Team>"; inside their Team's room the member name
// is enough.
function shortName(a: LabChartAgent, team: string) {
  const suffix = ` · ${team}`;
  return a.source === 'laboratory' && a.name.endsWith(suffix)
    ? a.name.slice(0, -suffix.length)
    : a.name;
}
const entities = computed(() => {
  const out = new Map<string, Entity>();
  for (const room of shown.value.rooms)
    for (const agent of room.agents)
      out.set(`a:${agent.id}`, {
        key: `a:${agent.id}`,
        kind: 'agent',
        agent,
        stage: room.stage,
        name: shortName(agent, room.team),
      });
  for (const person of shown.value.people)
    out.set(`p:${person.user_id}`, { key: `p:${person.user_id}`, kind: 'person', person });
  return out;
});

const canvas = ref<HTMLCanvasElement>();
const failed = ref(false);
const hovered = ref<string | null>(null);
const selected = ref<string | null>(null);
const zoomedIn = ref(false);
const pos = reactive<Record<string, { x: number; y: number }>>({});

const pills = computed(() => {
  if (props.preview) return [];
  return [...entities.value.values()].flatMap((e) => {
    if (e.kind === 'person') {
      const show = (e.person.user_id === props.me && zoomedIn.value) || hovered.value === e.key;
      return show ? [{ key: e.key, cls: 'human', name: e.person.name, dot: '', tail: '' }] : [];
    }
    const a = e.agent;
    if (a.status === 'idle' && hovered.value !== e.key) return [];
    if (a.status === 'needs_you')
      return [{ key: e.key, cls: 'ask', name: e.name, dot: '', tail: ' · needs your answer' }];
    const dot = hex(a.role === 'auditor' ? AUDITOR : COL[e.stage]);
    const work = a.studies.length > 1 ? `${a.studies.length} studies` : a.studies[0]?.title;
    return [
      {
        key: e.key,
        cls: '',
        name: e.name,
        dot,
        tail: a.status === 'working' && work ? ` · ${work}` : '',
      },
    ];
  });
});

const HINT = 'drag to look around · wheel to zoom · click an agent or a person for details';
const CLI = { claude: 'Claude Code', codex: 'Codex' };
function lasted(since: string | null) {
  if (!since) return '';
  const m = Math.max(1, Math.round((Date.now() - Date.parse(since)) / 60_000));
  return m < 60 ? `${m} min` : `${Math.floor(m / 60)} h`;
}
const panel = computed(() => {
  const e = selected.value ? entities.value.get(selected.value) : undefined;
  if (!e) return null;
  type Row = { label: string; value: string[]; tone?: 'ask' | 'work' };
  if (e.kind === 'person') {
    const p = e.person;
    const waiting = p.open_decisions
      ? `${p.open_decisions} answer${p.open_decisions === 1 ? '' : 's'}`
      : 'nothing';
    const rows: Row[] = props.preview
      ? []
      : [
          { label: 'Role', value: [p.role] },
          { label: 'Waiting on them', value: [waiting] },
        ];
    return {
      name: p.name,
      role: 'researcher',
      rows,
      link: props.preview
        ? null
        : { text: 'Open profile →', go: () => emit('open-person', p.user_id) },
    };
  }
  const a = e.agent;
  const rows: Row[] = [{ label: 'Room', value: [STAGE_LABEL[e.stage]] }];
  if (!props.preview) {
    const t = lasted(a.since);
    const status: Row =
      a.status === 'needs_you'
        ? { label: 'Status', value: [t ? `Needs you · waiting ${t}` : 'Needs you'], tone: 'ask' }
        : a.status === 'working'
          ? { label: 'Status', value: [t ? `Working · ${t}` : 'Working'], tone: 'work' }
          : { label: 'Status', value: ['Idle'] };
    rows.push(
      { label: 'Reports to', value: [a.reports_to] },
      status,
      {
        label: 'Studies',
        value: a.studies.length ? a.studies.map((s) => s.title) : ['none right now'],
      },
      {
        label: 'Machine',
        value: [
          a.machine
            ? [a.machine, a.cli && CLI[a.cli]].filter(Boolean).join(' · ')
            : 'Not bound to a machine',
        ],
      },
      {
        label: 'Made by',
        value: [a.source === 'researcher' ? 'a researcher' : 'built-in laboratory'],
      },
    );
  }
  return {
    name: e.name,
    role: a.role,
    rows,
    link: props.preview ? null : { text: 'Open agent →', go: () => emit('open-agent', a.id) },
  };
});

const ticker = computed(() => {
  const t = props.chart.ticker;
  if (props.preview || !t) return null;
  const at = new Date(t.at);
  return {
    time: `${String(at.getHours()).padStart(2, '0')}:${String(at.getMinutes()).padStart(2, '0')}`,
    text: t.text,
  };
});

// Orthographic camera on an isometric orbit: it opens zoomed out, eases in until the plan
// fills the canvas, then moves only when dragged or zoomed. zoom and goal are multiples of
// `fit`, the zoom at which the whole plan fits.
const VIEW = 150;
const THETA = Math.PI / 4,
  PHI = 0.96;
const camera = new THREE.OrthographicCamera(-1, 1, 1, -1, -500, 1000);
const target = new THREE.Vector3(0, 0, -4);
let theta = THETA,
  phi = PHI,
  zoom = 0.45,
  goal = 0.45,
  fit = 1;
let drag: { x: number; y: number; moved: boolean } | null = null;
let renderer: THREE.WebGLRenderer | null = null;
const scene = new THREE.Scene();
const rooms = shallowRef<Rooms | null>(null);
let easeIn: ReturnType<typeof setTimeout> | undefined;
let raf = 0,
  w = 0,
  h = 0;

function placeCam() {
  const R = 300;
  camera.position.set(
    target.x + R * Math.sin(phi) * Math.sin(theta),
    target.y + R * Math.cos(phi),
    target.z + R * Math.sin(phi) * Math.cos(theta),
  );
  camera.lookAt(target);
  camera.zoom = zoom * fit;
  camera.updateProjectionMatrix();
}

function refit() {
  if (!rooms.value || !w || !h) return;
  const f = fitView(rooms.value.group, THETA, PHI, VIEW, w / h);
  fit = f.zoom;
  target.copy(f.target);
}

// Text planes are drawn once per label and reused by every rebuild; the scene's disposal
// on unmount releases them.
const texts = new Map<string, THREE.Mesh>();
function cachedText(key: string, make: () => THREE.Mesh) {
  let m = texts.get(key);
  if (!m) texts.set(key, (m = make()));
  return m;
}

function rebuild() {
  if (!renderer) return;
  if (rooms.value) {
    for (const m of texts.values()) m.removeFromParent();
    scene.remove(rooms.value.group);
    disposeObject(rooms.value.group);
  }
  rooms.value = buildRooms(shown.value.rooms, shown.value.people, cachedText);
  scene.add(rooms.value.group);
  refit();
  if (selected.value && !entities.value.has(selected.value)) selected.value = null;
}

const v = new THREE.Vector3();
function tick() {
  raf = requestAnimationFrame(tick);
  const el = canvas.value!;
  if (el.clientWidth !== w || el.clientHeight !== h) {
    w = el.clientWidth;
    h = el.clientHeight;
    renderer!.setSize(w, h, false);
    const aspect = w / (h || 1);
    Object.assign(camera, {
      left: (-VIEW * aspect) / 2,
      right: (VIEW * aspect) / 2,
      top: VIEW / 2,
      bottom: -VIEW / 2,
    });
    refit();
  }
  zoom += (goal - zoom) * 0.08;
  placeCam();
  zoomedIn.value = zoom > 0.7;
  const anchors = rooms.value?.labelled ?? [];
  for (const p of pills.value) {
    const l = anchors.find((a) => a.key === p.key);
    if (!l) continue;
    v.copy(l.anchor).project(camera);
    const at = (pos[p.key] ??= { x: 0, y: 0 });
    at.x = Math.round(((v.x + 1) / 2) * w);
    at.y = Math.round(((1 - v.y) / 2) * h);
  }
  renderer!.render(scene, camera);
}

const ray = new THREE.Raycaster();
const mouse = new THREE.Vector2();
function hit(e: PointerEvent): string | null {
  const r = canvas.value!.getBoundingClientRect();
  mouse.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
  ray.setFromCamera(mouse, camera);
  const found = ray.intersectObjects(rooms.value?.pickables ?? [])[0];
  return (found?.object.userData.key as string) ?? null;
}
function down(e: PointerEvent) {
  drag = { x: e.clientX, y: e.clientY, moved: false };
  (e.currentTarget as HTMLCanvasElement).setPointerCapture(e.pointerId);
}
function move(e: PointerEvent) {
  if (drag) {
    const dx = e.clientX - drag.x,
      dy = e.clientY - drag.y;
    if (Math.abs(dx) + Math.abs(dy) > 3) drag.moved = true;
    theta -= dx * 0.005;
    phi = Math.min(1.3, Math.max(0.35, phi - dy * 0.005));
    drag.x = e.clientX;
    drag.y = e.clientY;
  }
  hovered.value = hit(e);
}
function up(e: PointerEvent) {
  if (drag && !drag.moved) {
    const key = hit(e);
    if (key) selected.value = key;
  }
  drag = null;
}
function wheel(e: WheelEvent) {
  goal = Math.min(6, Math.max(0.35, goal * (1 - e.deltaY * 0.0015)));
}

onMounted(() => {
  try {
    renderer = new THREE.WebGLRenderer({ canvas: canvas.value, antialias: true });
  } catch {
    failed.value = true;
    return;
  }
  renderer.outputColorSpace = THREE.LinearSRGBColorSpace;
  renderer.setPixelRatio(globalThis.devicePixelRatio || 1);
  renderer.shadowMap.enabled = true;
  renderer.shadowMap.type = THREE.PCFShadowMap;
  addSurroundings(scene);
  rebuild();
  raf = requestAnimationFrame(tick);
  easeIn = setTimeout(() => (goal = 1), 600);
});
watch(shown, rebuild);
onBeforeUnmount(() => {
  cancelAnimationFrame(raf);
  clearTimeout(easeIn);
  disposeObject(scene);
  renderer?.dispose();
  renderer = null;
});
</script>

<template>
  <div class="lab-chart" :style="{ height }">
    <canvas
      v-if="!failed"
      ref="canvas"
      class="lab-chart-canvas"
      :style="{ cursor: hovered ? 'pointer' : 'grab' }"
      aria-hidden="true"
      @pointerdown="down"
      @pointermove="move"
      @pointerup="up"
      @pointerleave="hovered = null"
      @wheel.prevent="wheel"
    />
    <div class="lab-chart-hint">{{ HINT }}</div>
    <template v-if="!failed">
      <div
        v-for="p in pills"
        :key="p.key"
        :class="['lab-chart-pill', p.cls]"
        :style="
          pos[p.key] ? { left: `${pos[p.key].x}px`, top: `${pos[p.key].y}px` } : { display: 'none' }
        "
      >
        <span v-if="p.dot" class="lab-chart-pill-dot" :style="{ background: p.dot }" /><b>{{
          p.name
        }}</b
        >{{ p.tail }}
      </div>
    </template>
    <div v-if="ticker" class="lab-chart-tick">
      <span class="lab-chart-tick-time">{{ ticker.time }}</span
      >{{ ticker.text }}
    </div>
    <div v-if="panel" class="lab-chart-panel">
      <button
        type="button"
        class="lab-chart-panel-close"
        aria-label="Close"
        @click="selected = null"
      >
        ✕
      </button>
      <h4 class="lab-chart-panel-name">{{ panel.name }}</h4>
      <div class="lab-chart-panel-role">{{ panel.role }}</div>
      <div v-for="r in panel.rows" :key="r.label" class="lab-chart-panel-row">
        <span>{{ r.label }}</span>
        <span :class="r.tone"
          ><template v-for="(line, i) in r.value" :key="i"
            ><br v-if="i" />{{ line }}</template
          ></span
        >
      </div>
      <button v-if="panel.link" type="button" class="lab-chart-panel-link" @click="panel.link.go">
        {{ panel.link.text }}
      </button>
    </div>
    <LabChartList
      :chart="shown"
      :preview="preview"
      :hidden="!failed"
      @open-agent="(id) => emit('open-agent', id)"
      @open-person="(id) => emit('open-person', id)"
    />
  </div>
</template>

<style src="./lab-chart.css" />
