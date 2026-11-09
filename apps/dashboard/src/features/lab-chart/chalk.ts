import * as THREE from 'three';
import type { LabChartRoom } from '../../api/types';
import { fitFontSize } from './text-fit';

type Stage = LabChartRoom['stage'];
type Box = { x: number; y: number; w: number; h: number };
type Pen = {
  g: CanvasRenderingContext2D;
  line: (pts: [number, number][]) => void;
  label: (text: string, x: number, y: number, size: number, box: Box) => void;
};
// A board is four cells: either lines of handwriting or a small chalk drawing.
type Cell = { lines: string[] } | { sketch: string; draw: (p: Pen, b: Box) => void };

const CHALK = '#eef2ec';
const HAND = 'Caveat, cursive';

const at = (b: Box, fx: number, fy: number): [number, number] => [b.x + fx * b.w, b.y + fy * b.h];

function arrowHead(p: Pen, [x, y]: [number, number], angle: number, size: number) {
  p.line([
    [x - size * Math.cos(angle - 0.5), y - size * Math.sin(angle - 0.5)],
    [x, y],
    [x - size * Math.cos(angle + 0.5), y - size * Math.sin(angle + 0.5)],
  ]);
}

function axes(p: Pen, b: Box) {
  p.line([at(b, 0.08, 0.05), at(b, 0.08, 0.9), at(b, 0.98, 0.9)]);
}

const SKETCHES = {
  graph: (p: Pen, b: Box) => {
    const nodes: [number, number][] = [
      [0.12, 0.3],
      [0.45, 0.12],
      [0.85, 0.3],
      [0.3, 0.82],
      [0.72, 0.8],
    ];
    for (const [i, j] of [
      [0, 1],
      [1, 2],
      [0, 3],
      [1, 3],
      [1, 4],
      [2, 4],
      [3, 4],
    ])
      p.line([at(b, ...nodes[i]), at(b, ...nodes[j])]);
    nodes.forEach((n, i) => {
      const [x, y] = at(b, ...n);
      p.g.beginPath();
      p.g.arc(x, y, b.h * 0.07, 0, Math.PI * 2);
      p.g.fill();
      p.label(`[${i + 1}]`, x + b.h * 0.1, y - b.h * 0.14, b.h * 0.18, b);
    });
  },
  tree: (p: Pen, b: Box) => {
    const root = at(b, 0.5, 0.14);
    const kids = [at(b, 0.25, 0.52), at(b, 0.75, 0.52)];
    for (const k of kids) p.line([[root[0], root[1] + b.h * 0.08], [k[0], k[1] - b.h * 0.1]]);
    for (const [i, k] of kids.entries())
      for (const fx of [-0.12, 0.12]) {
        const leaf = at(b, (i ? 0.75 : 0.25) + fx, 0.88);
        p.line([[k[0], k[1] + b.h * 0.1], leaf]);
        p.g.beginPath();
        p.g.arc(leaf[0], leaf[1], b.h * 0.05, 0, Math.PI * 2);
        p.g.fill();
      }
    p.label('H', root[0] - b.h * 0.05, root[1], b.h * 0.22, b);
    p.label('H₀', kids[0][0] - b.h * 0.1, kids[0][1], b.h * 0.22, b);
    p.label('H₁', kids[1][0] - b.h * 0.1, kids[1][1], b.h * 0.22, b);
  },
  bell: (p: Pen, b: Box) => {
    axes(p, b);
    const pts: [number, number][] = [];
    for (let i = 0; i <= 40; i++) {
      const t = (i / 40) * 6 - 3;
      pts.push(at(b, 0.12 + (i / 40) * 0.84, 0.88 - 0.62 * Math.exp((-t * t) / 2)));
    }
    p.line(pts);
    p.g.setLineDash([10, 10]);
    p.line([at(b, 0.8, 0.88), at(b, 0.8, 0.5)]);
    p.g.setLineDash([]);
    p.label('p < 0.05', b.x + b.w * 0.56, b.y + b.h * 0.14, b.h * 0.2, b);
  },
  flask: (p: Pen, b: Box) => {
    p.line([
      at(b, 0.42, 0.06),
      at(b, 0.42, 0.36),
      at(b, 0.2, 0.9),
      at(b, 0.8, 0.9),
      at(b, 0.58, 0.36),
      at(b, 0.58, 0.06),
    ]);
    p.line([at(b, 0.32, 0.62), at(b, 0.68, 0.62)]);
    for (const [fx, fy, r] of [
      [0.45, 0.75, 0.04],
      [0.55, 0.7, 0.03],
      [0.5, 0.5, 0.025],
    ]) {
      const [x, y] = at(b, fx, fy);
      p.g.beginPath();
      p.g.arc(x, y, b.h * r, 0, Math.PI * 2);
      p.g.stroke();
    }
    p.label('37 °C', b.x + b.w * 0.66, b.y + b.h * 0.22, b.h * 0.18, b);
  },
  table: (p: Pen, b: Box) => {
    const rows = [
      ['n', 'x', 'y'],
      ['1', '2.1', '4.3'],
      ['2', '3.0', '6.2'],
      ['3', '3.9', '7.8'],
    ];
    const cw = b.w / 3,
      rh = b.h / rows.length;
    p.line([
      [b.x, b.y + rh],
      [b.x + b.w, b.y + rh],
    ]);
    p.line([
      [b.x + cw, b.y],
      [b.x + cw, b.y + b.h],
    ]);
    rows.forEach((row, r) =>
      row.forEach((cell, c) =>
        p.label(cell, b.x + c * cw + cw * 0.25, b.y + (r + 0.5) * rh, rh * 0.7, b),
      ),
    );
  },
  scatter: (p: Pen, b: Box) => {
    axes(p, b);
    for (const [fx, fy] of [
      [0.18, 0.78],
      [0.28, 0.7],
      [0.36, 0.72],
      [0.46, 0.55],
      [0.55, 0.5],
      [0.64, 0.42],
      [0.72, 0.36],
      [0.84, 0.24],
    ]) {
      const [x, y] = at(b, fx, fy);
      p.g.beginPath();
      p.g.arc(x, y, b.h * 0.03, 0, Math.PI * 2);
      p.g.fill();
    }
    p.line([at(b, 0.12, 0.84), at(b, 0.94, 0.18)]);
    p.label('y = βx + ε', b.x + b.w * 0.14, b.y + b.h * 0.12, b.h * 0.2, b);
  },
  interval: (p: Pen, b: Box) => {
    p.line([at(b, 0.02, 0.9), at(b, 0.98, 0.9)]);
    p.g.setLineDash([10, 10]);
    p.line([at(b, 0.3, 0.1), at(b, 0.3, 0.9)]);
    p.g.setLineDash([]);
    for (const [lo, mid, hi, fy] of [
      [0.42, 0.58, 0.74, 0.3],
      [0.36, 0.55, 0.8, 0.5],
      [0.46, 0.6, 0.72, 0.7],
    ]) {
      p.line([at(b, lo, fy), at(b, hi, fy)]);
      for (const f of [lo, hi]) p.line([at(b, f, fy - 0.06), at(b, f, fy + 0.06)]);
      const [x, y] = at(b, mid, fy);
      p.g.beginPath();
      p.g.arc(x, y, b.h * 0.035, 0, Math.PI * 2);
      p.g.fill();
    }
    p.label('95% CI', b.x + b.w * 0.6, b.y + b.h * 0.1, b.h * 0.18, b);
  },
  loop: (p: Pen, b: Box) => {
    const [cx, cy] = at(b, 0.5, 0.5);
    const r = Math.min(b.w, b.h) * 0.36;
    for (let k = 0; k < 3; k++) {
      const a0 = (k * 2 * Math.PI) / 3 + 0.25,
        a1 = a0 + (2 * Math.PI) / 3 - 0.5;
      const pts: [number, number][] = [];
      for (let i = 0; i <= 12; i++) {
        const a = a0 + ((a1 - a0) * i) / 12;
        pts.push([cx + r * Math.cos(a), cy + r * Math.sin(a)]);
      }
      p.line(pts);
      arrowHead(p, pts[12], a1 + Math.PI / 2, b.h * 0.1);
    }
    p.label('run', cx + r * 0.95, cy - r * 0.95, b.h * 0.17, b);
    p.label('rerun', cx - r * 2.1, cy + r * 1.0, b.h * 0.17, b);
    p.label('match?', cx + r * 0.8, cy + r * 1.0, b.h * 0.17, b);
  },
} satisfies Record<string, (p: Pen, b: Box) => void>;

const sketch = (name: keyof typeof SKETCHES): Cell => ({ sketch: name, draw: SKETCHES[name] });

export const BOARDS: Record<Stage, Cell[]> = {
  grounding: [
    { lines: ['[1] Lee et al., 2019', '[2] Ono & Park, 2021', '[3] Ruiz, 2023'] },
    sketch('graph'),
    { lines: ['P(H|E) =', 'P(E|H) · P(H) / P(E)'] },
    { lines: ['claim ← [1] [2]', 'n = 42 sources'] },
  ],
  hypotheses: [
    sketch('tree'),
    { lines: ['H₀: μ₁ = μ₂', 'H₁: μ₁ ≠ μ₂'] },
    { lines: ['if sleep ↑', 'then recall ↑'] },
    sketch('bell'),
  ],
  experimentation: [
    sketch('flask'),
    sketch('table'),
    sketch('scatter'),
    { lines: ['σ = √(Σ(x−μ)²/N)', '∂u/∂t = α∇²u', 'E = mc²'] },
  ],
  validation: [
    sketch('interval'),
    { lines: ['✓ rerun matches', '✓ effect holds', '✗ p = 0.21'] },
    sketch('loop'),
    { lines: ['t = (x̄ − μ) / (s/√n)', 'p < 0.05'] },
  ],
};

// A deterministic wobble, so a board looks hand-drawn yet is the same every time it is drawn.
function wobble(seed: number) {
  let s = seed;
  return () => {
    s = (s * 1103515245 + 12345) % 2147483648;
    return (s / 2147483648 - 0.5) * 3;
  };
}

// A plane covered in chalk writing for a stage's board, drawn now and again once Caveat has
// loaded. `userData.writing` lists what is on it.
export function chalkPlane(stage: Stage, w: number, h: number) {
  const cells = BOARDS[stage];
  const cv = document.createElement('canvas');
  cv.width = 2048;
  cv.height = Math.round((2048 * h) / w);
  const g = cv.getContext('2d')!;
  const tex = new THREE.CanvasTexture(cv);
  const draw = () => {
    const jitter = wobble(stage.length * 7919);
    g.clearRect(0, 0, cv.width, cv.height);
    g.strokeStyle = g.fillStyle = CHALK;
    g.globalAlpha = 0.9;
    g.lineWidth = 5;
    g.lineCap = g.lineJoin = 'round';
    g.textBaseline = 'middle';
    const measure = (text: string, size: number) => {
      g.font = `600 ${size}px ${HAND}`;
      return g.measureText(text).width;
    };
    const pen: Pen = {
      g,
      line(pts) {
        g.beginPath();
        pts.forEach(([x, y], i) => (i ? g.lineTo : g.moveTo).call(g, x + jitter(), y + jitter()));
        g.stroke();
      },
      // A label shrinks to fit its box, then moves left so that it ends inside it.
      label(text, x, y, size, box) {
        const fit = Math.min(size, box.h, (box.w * 0.98) / Math.max(1e-6, measure(text, 1)));
        const width = measure(text, fit);
        g.textAlign = 'left';
        g.fillText(
          text,
          Math.max(box.x, Math.min(x, box.x + box.w - width)),
          Math.max(box.y + fit / 2, Math.min(y, box.y + box.h - fit / 2)),
        );
      },
    };
    const pad = cv.height * 0.12;
    const cw = cv.width / cells.length;
    cells.forEach((cell, i) => {
      const box = { x: i * cw + pad, y: pad, w: cw - 2 * pad, h: cv.height - 2 * pad };
      if ('draw' in cell) return cell.draw(pen, box);
      const lh = box.h / cell.lines.length;
      const size = Math.min(
        ...cell.lines.map((l) => fitFontSize(l, (sz) => measure(l, sz), box.w, lh, 0.82, 0.98)),
      );
      g.font = `600 ${size}px ${HAND}`;
      g.textAlign = 'center';
      cell.lines.forEach((l, k) => g.fillText(l, box.x + box.w / 2, box.y + (k + 0.5) * lh));
    });
    tex.needsUpdate = true;
  };
  const mesh = new THREE.Mesh(
    new THREE.PlaneGeometry(w, h),
    new THREE.MeshBasicMaterial({ map: tex, transparent: true }),
  );
  draw();
  const fonts = document.fonts;
  if (fonts) {
    fonts.load(`600 40px ${HAND}`).then(draw, () => {});
    fonts.ready.then(draw);
  }
  mesh.userData.board = stage;
  mesh.userData.writing = cells.map((c) => ('lines' in c ? c.lines.join('\n') : `(${c.sketch})`));
  return mesh;
}
