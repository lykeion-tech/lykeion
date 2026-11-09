import * as THREE from 'three';
import type { LabChartAgent, LabChartPerson, LabChartRoom } from '../../api/types';
import { layoutConference, layoutRoom, placeRooms, ROOM_MIN_DEPTH, ROOM_WIDTH } from './layout';
import { fitFontSize } from './text-fit';

type Stage = LabChartRoom['stage'];

// The look is tuned for unmanaged colours: hex values reach the screen as written and
// light intensities are scaled by π to match the legacy lighting model.
THREE.ColorManagement.enabled = false;

export const STAGE_LABEL: Record<Stage, string> = {
  grounding: 'Grounding',
  hypotheses: 'Hypotheses',
  experimentation: 'Experimentation',
  validation: 'Validation',
};
export const STAGES = Object.keys(STAGE_LABEL) as Stage[];
export const COL: Record<Stage, number> = {
  grounding: 0x8b93a8,
  hypotheses: 0xa57bc9,
  experimentation: 0xd4a24a,
  validation: 0x4fa56b,
};
const FLOOR: Record<Stage, number> = {
  grounding: 0xdfe2ea,
  hypotheses: 0xece0f4,
  experimentation: 0xf3e6cc,
  validation: 0xd9ecdf,
};
export const AUDITOR = 0xc8c8c4;
const PERSON = 0x2f3e57;
const PERSON_HEAD = 0xe8cdb0;
const LEADER_LINE = 0xc8302f;
const LAMP = { idle: 0x6a6b6f, working: 0xf5a524, needs_you: 0xe5484d };
const BRICK = 0x9c5a44,
  CREAM = 0xe9dcb8,
  ROOF = 0x9b8170,
  GLASS = 0x8fb8d8,
  WALL = 0xefe9dd,
  WH = 4.2;
export const BACKGROUND = 0xf6f6f4;
export const hex = (c: number) => '#' + c.toString(16).padStart(6, '0');

const mat = (c: number) => new THREE.MeshLambertMaterial({ color: c });
function add(
  parent: THREE.Object3D,
  geo: THREE.BufferGeometry,
  m: THREE.Material,
  x: number,
  y: number,
  z: number,
  shadow = true,
) {
  const o = new THREE.Mesh(geo, m);
  o.position.set(x, y, z);
  o.castShadow = shadow;
  o.receiveShadow = true;
  parent.add(o);
  return o;
}

// A plane carrying text, sized by fitFontSize to stay inside its surface. With `split`, the
// last word goes on a second line and both lines share the size that fits the wider one.
// The texture is drawn now and again once the web font has loaded.
export function textPlane(
  text: string,
  w: number,
  h: number,
  o: {
    weight: number;
    family: string;
    color: string;
    bg?: string;
    fill: number;
    margin?: number;
    split?: boolean;
  },
) {
  const words = text.split(' ');
  const lines =
    o.split && words.length > 1 ? [words.slice(0, -1).join(' '), words[words.length - 1]] : [text];
  const cv = document.createElement('canvas');
  cv.width = 2048;
  cv.height = Math.round((2048 * h) / w);
  const g = cv.getContext('2d')!;
  const tex = new THREE.CanvasTexture(cv);
  const draw = () => {
    g.clearRect(0, 0, cv.width, cv.height);
    if (o.bg) {
      g.fillStyle = o.bg;
      g.fillRect(0, 0, cv.width, cv.height);
    }
    const width = (line: string, size: number) => {
      g.font = `${o.weight} ${size}px ${o.family}`;
      return g.measureText(line).width;
    };
    const wide = lines.reduce((a, b) => (width(b, 100) > width(a, 100) ? b : a));
    const lineHeight = cv.height / lines.length;
    const size = fitFontSize(wide, (sz) => width(wide, sz), cv.width, lineHeight, o.fill, o.margin);
    mesh.userData.fontSize = (size * h) / cv.height;
    g.font = `${o.weight} ${size}px ${o.family}`;
    g.fillStyle = o.color;
    g.textAlign = 'center';
    g.textBaseline = 'middle';
    lines.forEach((line, i) =>
      g.fillText(line, cv.width / 2, cv.height / 2 + (i - (lines.length - 1) / 2) * size),
    );
    tex.needsUpdate = true;
  };
  const mesh = new THREE.Mesh(
    new THREE.PlaneGeometry(w, h),
    new THREE.MeshBasicMaterial({ map: tex, transparent: !o.bg }),
  );
  draw();
  const fonts = document.fonts;
  if (fonts) {
    fonts.load(`${o.weight} 40px ${o.family}`).then(draw, () => {});
    fonts.ready.then(draw);
  }
  mesh.userData.text = text;
  mesh.userData.lines = lines;
  return mesh;
}

// The ground sheet, the closed lab building and the lights: everything that does not
// depend on the chart.
export function addSurroundings(scene: THREE.Scene) {
  scene.background = new THREE.Color(BACKGROUND);
  scene.add(new THREE.HemisphereLight(0xffffff, 0x9a9a9a, 0.9 * Math.PI));
  const sun = new THREE.DirectionalLight(0xffffff, 0.55 * Math.PI);
  sun.position.set(-40, 90, 60);
  sun.castShadow = true;
  sun.shadow.mapSize.set(4096, 4096);
  Object.assign(sun.shadow.camera, { left: -90, right: 90, top: 90, bottom: -90, far: 300 });
  scene.add(sun);

  const cv = document.createElement('canvas');
  cv.width = cv.height = 256;
  const g = cv.getContext('2d')!;
  g.fillStyle = '#f6f6f4';
  g.fillRect(0, 0, 256, 256);
  g.strokeStyle = '#e3e3df';
  g.lineWidth = 2;
  g.strokeRect(0, 0, 256, 256);
  g.fillStyle = '#cfcfca';
  g.beginPath();
  g.arc(0, 0, 4, 0, 7);
  g.fill();
  const t = new THREE.CanvasTexture(cv);
  t.wrapS = t.wrapT = THREE.RepeatWrapping;
  t.repeat.set(60, 60);
  add(
    scene,
    new THREE.PlaneGeometry(400, 400),
    new THREE.MeshLambertMaterial({ map: t }),
    0,
    0,
    0,
    false,
  ).rotation.x = -Math.PI / 2;

  const block = (x: number, z: number, w: number, d: number, h: number) => {
    add(scene, new THREE.BoxGeometry(w, 1.2, d), mat(BRICK), x, 0.6, z);
    add(scene, new THREE.BoxGeometry(w, h - 1.2, d), mat(CREAM), x, 1.2 + (h - 1.2) / 2, z);
    add(scene, new THREE.BoxGeometry(w + 0.4, 0.4, d + 0.4), mat(0xd9c9a0), x, h + 0.2, z);
    add(scene, new THREE.BoxGeometry(w - 0.6, 0.1, d - 0.6), mat(ROOF), x, h + 0.45, z, false);
    for (let i = 0; i < Math.floor(w / 3.2); i++)
      add(
        scene,
        new THREE.BoxGeometry(2.2, h - 2.2, 0.1),
        mat(GLASS),
        x - w / 2 + 1.8 + i * 3.2,
        1.2 + (h - 1.2) / 2,
        z + d / 2 + 0.05,
        false,
      );
    for (let i = 0; i < Math.floor(d / 3.2); i++)
      add(
        scene,
        new THREE.BoxGeometry(0.1, h - 2.2, 2.2),
        mat(GLASS),
        x + w / 2 + 0.05,
        1.2 + (h - 1.2) / 2,
        z - d / 2 + 1.8 + i * 3.2,
        false,
      );
  };
  block(0, 0, 30, 20, 5);
  block(-4, -14, 18, 8, 4.2);
  block(16, -4, 8, 14, 6);
  for (let i = 0; i < 3; i++)
    add(scene, new THREE.BoxGeometry(2.4, 0.3, 1.4), mat(GLASS), -6 + i * 5, 5.6, -3, false);
  add(scene, new THREE.BoxGeometry(7, 0.3, 4.5), mat(GLASS), 3, 5.6, 3, false);
}

// The condensed face keeps the longest Team name readable on a TV that fits the wall.
const TV = {
  weight: 700,
  family: "'Barlow Condensed', Barlow, sans-serif",
  color: '#ffffff',
  bg: '#2c4f6b',
  fill: 0.95,
  margin: 0.98,
  split: true,
};

// The camera zoom at which every mesh of `group` fits a view `view` units high (at zoom 1)
// with `margin` to spare, seen from the orbit angles theta and phi, and the point the
// camera must look at to centre them.
export function fitView(
  group: THREE.Object3D,
  theta: number,
  phi: number,
  view: number,
  aspect: number,
  margin = 1.05,
) {
  const cam = new THREE.OrthographicCamera();
  cam.position.set(Math.sin(phi) * Math.sin(theta), Math.cos(phi), Math.sin(phi) * Math.cos(theta));
  cam.lookAt(0, 0, 0);
  cam.updateMatrixWorld();
  const toView = cam.matrixWorldInverse;
  const box = new THREE.Box3();
  const p = new THREE.Vector3();
  let x0 = Infinity,
    x1 = -Infinity,
    y0 = Infinity,
    y1 = -Infinity;
  group.updateMatrixWorld(true);
  group.traverse((o) => {
    if (!(o instanceof THREE.Mesh)) return;
    box.setFromObject(o);
    for (let i = 0; i < 8; i++) {
      p.set(
        i & 1 ? box.max.x : box.min.x,
        i & 2 ? box.max.y : box.min.y,
        i & 4 ? box.max.z : box.min.z,
      );
      p.applyMatrix4(toView);
      x0 = Math.min(x0, p.x);
      x1 = Math.max(x1, p.x);
      y0 = Math.min(y0, p.y);
      y1 = Math.max(y1, p.y);
    }
  });
  if (x0 > x1) return { zoom: 1, target: new THREE.Vector3() };
  const target = new THREE.Vector3()
    .setFromMatrixColumn(cam.matrixWorld, 0)
    .multiplyScalar((x0 + x1) / 2)
    .add(new THREE.Vector3().setFromMatrixColumn(cam.matrixWorld, 1).multiplyScalar((y0 + y1) / 2));
  const zoom = Math.min((view * aspect) / ((x1 - x0) * margin), view / ((y1 - y0) * margin));
  return { zoom, target };
}

export type Labelled = { key: string; anchor: THREE.Vector3 };
// A room's hover area is its floor and walls; `outline` is the glow shown while it is hovered.
export type RoomArea = { key: string; title: string; line: string; outline: THREE.Object3D };
export type Rooms = {
  group: THREE.Group;
  pickables: THREE.Mesh[];
  labelled: Labelled[];
  areas: THREE.Mesh[];
  rooms: Map<string, RoomArea>;
};

// The five rooms pulled out of the building: one per stage, then the Conference room.
// `text` returns the plane for a label, so a caller can keep planes across rebuilds; the
// planes it hands out are not disposed with the rooms.
export function buildRooms(
  rooms: LabChartRoom[],
  people: LabChartPerson[],
  text: (key: string, make: () => THREE.Mesh) => THREE.Mesh,
): Rooms {
  const group = new THREE.Group();
  const pickables: THREE.Mesh[] = [];
  const labelled: Labelled[] = [];
  const areas: THREE.Mesh[] = [];
  const areaRooms = new Map<string, RoomArea>();
  const spots = placeRooms(5);

  const leader = (fx: number, fz: number, tx: number, ty: number, tz: number) => {
    const geo = new THREE.BufferGeometry().setFromPoints([
      new THREE.Vector3(fx, 0.7, fz),
      new THREE.Vector3(tx, ty, tz),
    ]);
    group.add(new THREE.Line(geo, new THREE.LineBasicMaterial({ color: LEADER_LINE })));
    add(
      group,
      new THREE.SphereGeometry(0.35, 10, 8),
      new THREE.MeshBasicMaterial({ color: LEADER_LINE }),
      tx,
      ty,
      tz,
      false,
    );
    add(
      group,
      new THREE.SphereGeometry(0.35, 10, 8),
      new THREE.MeshBasicMaterial({ color: LEADER_LINE }),
      fx,
      0.7,
      fz,
      false,
    );
  };
  const figure = (x: number, y: number, z: number, color: number, head = 0xf1e4d4) => {
    add(group, new THREE.CylinderGeometry(0.32, 0.32, 0.8, 16), mat(color), x, y + 0.6, z);
    add(group, new THREE.SphereGeometry(0.32, 16, 12), mat(color), x, y + 1.0, z);
    add(group, new THREE.SphereGeometry(0.27, 16, 12), mat(head), x, y + 1.45, z);
    return y + 1.45;
  };
  const hitbox = (x: number, y: number, z: number, key: string, top: number) => {
    const hb = add(
      group,
      new THREE.BoxGeometry(1.4, 1.9, 1.4),
      new THREE.MeshBasicMaterial({ visible: false }),
      x,
      y + 0.95,
      z,
      false,
    );
    hb.userData.key = key;
    pickables.push(hb);
    labelled.push({ key, anchor: new THREE.Vector3(x, top + 0.7, z) });
  };
  const bookshelf = (x: number, z: number, len: number) => {
    add(group, new THREE.BoxGeometry(len, 2.4, 0.5), mat(0x6b4f35), x, 0.6 + 1.2, z);
    const bc = [0x8c2f39, 0x2f5d8c, 0x3f7d4f, 0xc9a14a, 0x5a4a7a, 0xa65d3f];
    for (let s = 0; s < 3; s++)
      for (let i = 0; i < Math.floor(len / 0.32); i++) {
        const h = 0.45 + ((i * 7 + s * 3) % 4) * 0.06;
        add(
          group,
          new THREE.BoxGeometry(0.24, h, 0.34),
          mat(bc[(i + s * 2) % bc.length]),
          x - len / 2 + 0.2 + i * 0.32,
          0.6 + 0.25 + s * 0.75 + h / 2,
          z + 0.12,
          false,
        );
      }
  };
  const plant = (x: number, y: number, z: number) => {
    add(group, new THREE.CylinderGeometry(0.35, 0.28, 0.6, 12), mat(0xe9e4da), x, y + 0.3, z);
    add(group, new THREE.SphereGeometry(0.6, 12, 10), mat(0x4f8a4f), x, y + 1.05, z);
    add(group, new THREE.SphereGeometry(0.4, 12, 10), mat(0x5e9c5a), x + 0.25, y + 1.45, z - 0.1);
  };
  // A cutaway room: a floor slab with a visible edge, back and left walls, the front open.
  // Its back wall stays where a minimum-depth room's would be; a deeper room grows forward.
  const shell = (
    cx: number,
    spotZ: number,
    w: number,
    d: number,
    floorC: number,
    room: Omit<RoomArea, 'outline'>,
  ) => {
    const x1 = cx - w / 2,
      z1 = spotZ - ROOM_MIN_DEPTH / 2,
      cz = z1 + d / 2;
    const parts = [
      add(group, new THREE.BoxGeometry(w, 0.6, d), mat(0xd8d4cc), cx, 0.3, cz),
      add(group, new THREE.BoxGeometry(w, 0.04, d), mat(floorC), cx, 0.62, cz, false),
      add(group, new THREE.BoxGeometry(w, WH, 0.3), mat(WALL), cx, 0.6 + WH / 2, z1 + 0.15),
      add(group, new THREE.BoxGeometry(0.3, WH, d), mat(WALL), x1 + 0.15, 0.6 + WH / 2, cz),
    ];
    for (const m of parts) m.userData.room = room.key;
    parts[1].userData.floor = true;
    areas.push(...parts);
    // A soft glow in the room's colour around the floor's edge: a bright band with a faint
    // wider one around it.
    const outline = new THREE.Group();
    const color = room.key === 'r:conference' ? 0x7f95b5 : COL[room.key.slice(2) as Stage];
    for (const [band, opacity] of [
      [0.9, 0.25],
      [0.3, 0.9],
    ]) {
      const m = new THREE.MeshBasicMaterial({ color, transparent: true, opacity });
      for (const [bw, bd, bx, bz] of [
        [w + band, band, cx, z1],
        [w + band, band, cx, z1 + d],
        [band, d + band, x1, cz],
        [band, d + band, x1 + w, cz],
      ])
        add(outline, new THREE.BoxGeometry(bw, 0.05, bd), m, bx, 0.64, bz, false);
    }
    outline.visible = false;
    outline.userData.outline = room.key;
    group.add(outline);
    areaRooms.set(room.key, { ...room, outline });
    labelled.push({ key: room.key, anchor: new THREE.Vector3(cx, 0.6 + WH + 1.6, z1 + 0.15) });
    add(group, new THREE.BoxGeometry(w, 0.12, 0.34), mat(0x8a6a48), cx, 0.6 + WH, z1 + 0.15, false);
    add(group, new THREE.BoxGeometry(0.34, 0.12, d), mat(0x8a6a48), x1 + 0.15, 0.6 + WH, cz, false);
    for (let i = 0; i < 3; i++)
      add(
        group,
        new THREE.BoxGeometry(1.4, 2.6, 0.08),
        mat(GLASS),
        cx + w / 2 - 1.4 - i * 1.8,
        0.6 + 2.2,
        z1 + 0.33,
        false,
      );
    return { x1, z1, x2: cx + w / 2, z2: z1 + d };
  };
  // A TV on the back wall, in the clear span between the bookshelf and the windows.
  const tvOnWall = (x: number, z1: number, w: number, key: string, label: string) => {
    add(group, new THREE.BoxGeometry(w, 3, 0.14), mat(0x1b1c1e), x, 0.6 + 2.6, z1 + 0.38);
    const tv = text(key, () => textPlane(label, w - 0.1, 2.8, TV));
    tv.position.set(x, 0.6 + 2.6, z1 + 0.46);
    group.add(tv);
  };
  const desk = (x: number, z: number, a: LabChartAgent, stage: Stage, faceBack: boolean) => {
    const y = 0.62;
    add(group, new THREE.BoxGeometry(2.6, 0.14, 1.2), mat(0xc9a173), x, y + 1.0, z);
    for (const [lx, lz] of [
      [-1.2, -0.5],
      [1.2, -0.5],
      [-1.2, 0.5],
      [1.2, 0.5],
    ])
      add(group, new THREE.BoxGeometry(0.09, 1, 0.09), mat(0x3a3a3a), x + lx, y + 0.5, z + lz);
    add(
      group,
      new THREE.CylinderGeometry(0.04, 0.04, 0.6),
      mat(0x2b2b2b),
      x - 1.05,
      y + 1.35,
      z - 0.35,
      false,
    );
    add(
      group,
      new THREE.SphereGeometry(0.16, 12, 10),
      new THREE.MeshBasicMaterial({ color: LAMP[a.status] }),
      x - 1.05,
      y + 1.7,
      z - 0.35,
      false,
    );
    // Two papers per study, four to a row; more studies pile them higher.
    if (a.status !== 'idle')
      for (let i = 0; i < a.studies.length * 2; i++)
        add(
          group,
          new THREE.BoxGeometry(0.36, 0.12, 0.28),
          mat(a.status === 'needs_you' ? 0xe5484d : 0xe0679a),
          x - 0.35 + (i % 4) * 0.45,
          y + 1.13 + Math.floor(i / 4) * 0.12,
          z + 0.1,
        );
    const fz = faceBack ? z - 1.1 : z + 1.1;
    const top = figure(x, y, fz, a.role === 'auditor' ? AUDITOR : COL[stage]);
    hitbox(x, y, fz, `a:${a.id}`, top);
  };

  for (const room of rooms) {
    const spot = spots[STAGES.indexOf(room.stage)];
    if (!spot) continue;
    const layout = layoutRoom(room);
    const r = shell(spot.x, spot.z, layout.width, layout.depth, FLOOR[room.stage], {
      key: `r:${room.stage}`,
      title: room.team,
      line: `The room of the ${room.team}'s agents`,
    });
    leader(spot.x, spot.z, spot.roofX, spot.roofY, spot.roofZ);
    tvOnWall(spot.x - 0.4, r.z1, 8.2, `tv:${room.team}`, room.team);
    bookshelf(r.x1 + 3.2, r.z1 + 0.55, 3.4);
    plant(r.x2 - 1.2, 0.62, r.z2 - 1.2);
    const byId = new Map(room.agents.map((a) => [a.id, a]));
    for (const d of layout.desks)
      desk(r.x1 + d.x, r.z1 + d.z, byId.get(d.id)!, room.stage, d.faces === 'back');
  }

  const spot = spots[4];
  const conf = layoutConference(people.length);
  const r = shell(spot.x, spot.z, ROOM_WIDTH, conf.depth, 0xdfe4ea, {
    key: 'r:conference',
    title: 'Conference room',
    line: "Where the lab's researchers meet",
  });
  leader(spot.x, spot.z, spot.roofX, spot.roofY, spot.roofZ);
  tvOnWall(spot.x - 0.25, r.z1, 7.9, 'tv:Conference room', 'Conference room');
  for (const t of conf.tables) {
    add(
      group,
      new THREE.BoxGeometry(t.length, 0.2, 3.4),
      mat(0x7a5638),
      r.x1 + t.x,
      0.62 + 1.0,
      r.z1 + t.z,
    );
    for (const lx of [-(t.length / 2 - 1.5), t.length / 2 - 1.5])
      add(
        group,
        new THREE.CylinderGeometry(0.45, 0.6, 0.9, 12),
        mat(0x3a3a3a),
        r.x1 + t.x + lx,
        0.62 + 0.45,
        r.z1 + t.z,
      );
  }
  const tableZ = (z: number) =>
    conf.tables.reduce((a, t) => (Math.abs(t.z - z) < Math.abs(a - z) ? t.z : a), conf.tables[0].z);
  for (const s of conf.seats) {
    const x = r.x1 + s.x,
      z = r.z1 + s.z;
    add(group, new THREE.BoxGeometry(0.8, 0.5, 0.8), mat(0x2b3442), x, 0.62 + 0.5, z);
    add(
      group,
      new THREE.BoxGeometry(0.8, 0.8, 0.15),
      mat(0x2b3442),
      x,
      0.62 + 1.0,
      z + (s.z < tableZ(s.z) ? -0.4 : 0.4),
    );
  }
  bookshelf(r.x1 + 3.2, r.z1 + 0.55, 4);
  plant(r.x2 - 1.2, 0.62, r.z1 + 1.4);
  plant(r.x1 + 1.3, 0.62, r.z2 - 1.2);
  people.forEach((p, i) => {
    const s = conf.seats[i];
    const x = r.x1 + s.x,
      z = r.z1 + s.z;
    const top = figure(x, 0.62 + 0.3, z, PERSON, PERSON_HEAD);
    hitbox(x, 0.62 + 0.3, z, `p:${p.user_id}`, top);
  });

  return { group, pickables, labelled, areas, rooms: areaRooms };
}

export function disposeObject(root: THREE.Object3D) {
  root.traverse((o) => {
    const m = o as THREE.Mesh;
    m.geometry?.dispose();
    for (const material of ([] as THREE.Material[]).concat(m.material ?? [])) {
      (material as THREE.MeshBasicMaterial).map?.dispose();
      material.dispose();
    }
  });
}
