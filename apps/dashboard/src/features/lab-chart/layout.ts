export type RoomLayout = {
  width: number
  depth: number
  desks: { id: string; x: number; z: number; faces: 'front' | 'back' }[]
}

export const ROOM_WIDTH = 19
export const ROOM_MIN_DEPTH = 15

const MEMBER_XS = [6.5, 10.7, 14.9]

export function layoutRoom(room: { agents: { id: string; role: 'leader' | 'member' | 'auditor' }[] }): RoomLayout {
  const desks: RoomLayout['desks'] = []
  const members = room.agents.filter((a) => a.role === 'member')
  members.forEach((a, i) => {
    desks.push({ id: a.id, x: MEMBER_XS[i % 3], z: 7.2 + Math.floor(i / 3) * 3.6, faces: 'front' })
  })
  const lastRowZ = members.length ? 7.2 + Math.floor((members.length - 1) / 3) * 3.6 : 0
  const depth = Math.max(ROOM_MIN_DEPTH, lastRowZ + 4.6)
  for (const a of room.agents) {
    if (a.role === 'leader') desks.push({ id: a.id, x: 8, z: 3.2, faces: 'back' })
    if (a.role === 'auditor') desks.push({ id: a.id, x: 2.4, z: depth - 2.2, faces: 'front' })
  }
  return { width: ROOM_WIDTH, depth, desks }
}

// Room centres on the exploded plan (four stages, then the Conference room), with the
// point on the building roof each room's connector line meets.
const SPOTS: [number, number, number, number, number][] = [
  [-29.4, -26.5, -10, 4.6, -12],
  [2.1, -35.5, 0, 4.6, -16],
  [33.6, -23.5, 16, 6.4, -8],
  [33.6, 18.5, 10, 5.4, 6],
  [-30.9, 17, -15, 5.4, 6],
]

export function placeRooms(count: number) {
  return SPOTS.slice(0, count).map(([x, z, roofX, roofY, roofZ]) => ({ x, z, roofX, roofY, roofZ }))
}

const CONFERENCE_SPACING = 2.2
const TABLE_ROW_GAP = 7.5
const MAX_PER_SIDE = 7

// Tables run along x, five seats a side at minimum. Each extra pair of people lengthens a
// table by one seat spacing, up to seven a side; beyond that another table row is added
// below and the room deepens by one row gap.
export function layoutConference(people: number): {
  depth: number
  seats: { x: number; z: number }[]
  tables: { x: number; z: number; length: number }[]
} {
  const tableCount = Math.max(1, Math.ceil(people / (MAX_PER_SIDE * 2)))
  const seats: { x: number; z: number }[] = []
  const tables: { x: number; z: number; length: number }[] = []
  let left = people
  for (let t = 0; t < tableCount; t++) {
    const here = Math.min(left, MAX_PER_SIDE * 2)
    left -= here
    const perSide = Math.max(5, Math.ceil(here / 2))
    const zOff = t * TABLE_ROW_GAP
    tables.push({ x: ROOM_WIDTH / 2, z: 8.5 + zOff, length: 11 + (perSide - 5) * CONFERENCE_SPACING })
    const x0 = ROOM_WIDTH / 2 - ((perSide - 1) / 2) * CONFERENCE_SPACING
    for (let i = 0; i < perSide; i++) {
      const x = x0 + i * CONFERENCE_SPACING
      seats.push({ x, z: 6 + zOff }, { x, z: 11 + zOff })
    }
  }
  return { depth: ROOM_MIN_DEPTH + (tableCount - 1) * TABLE_ROW_GAP, seats, tables }
}
