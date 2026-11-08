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
  [-40, -34, -10, 4.6, -12],
  [2, -46, 0, 4.6, -16],
  [44, -30, 16, 6.4, -8],
  [44, 26, 10, 5.4, 6],
  [-42, 24, -15, 5.4, 6],
]

export function placeRooms(count: number) {
  return SPOTS.slice(0, count).map(([x, z, roofX, roofY, roofZ]) => ({ x, z, roofX, roofY, roofZ }))
}

const CONFERENCE_SPACING = 2.2

// The table runs along z; each extra pair of people adds one seat to each side and
// lengthens the room by one seat spacing.
export function layoutConference(people: number): { depth: number; seats: { x: number; z: number }[] } {
  const perSide = Math.max(5, Math.ceil(people / 2))
  const depth = ROOM_MIN_DEPTH + (perSide - 5) * CONFERENCE_SPACING
  const seats: { x: number; z: number }[] = []
  for (let i = 0; i < perSide; i++) {
    const z = depth / 2 + (i - (perSide - 1) / 2) * CONFERENCE_SPACING
    seats.push({ x: ROOM_WIDTH / 2 - 2.5, z }, { x: ROOM_WIDTH / 2 + 2.5, z })
  }
  return { depth, seats }
}
