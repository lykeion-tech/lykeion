import { describe, expect, it } from 'vitest'
import { layoutConference, layoutRoom, placeRooms } from './layout'
import { fitFontSize } from './text-fit'
import { toPreviewChart } from './preview'
import type { Laboratory } from '../../api/types'

type Rect = { x: number; z: number; w: number; d: number }
const overlap = (a: Rect, b: Rect) =>
  Math.abs(a.x - b.x) < (a.w + b.w) / 2 && Math.abs(a.z - b.z) < (a.d + b.d) / 2

describe('layoutRoom', () => {
  it('seats a small room in the minimum depth with the leader facing the TV', () => {
    const l = layoutRoom({
      agents: [
        { id: 'l', role: 'leader' },
        { id: 'm1', role: 'member' },
        { id: 'm2', role: 'member' },
        { id: 'a', role: 'auditor' },
      ],
    })
    expect(l.depth).toBe(15)
    const back = l.desks.filter((d) => d.faces === 'back')
    expect(back).toEqual([{ id: 'l', x: 8, z: 3.2, faces: 'back' }])
  })

  it('grows a crowded room without overlapping desks', () => {
    const agents = [
      { id: 'l', role: 'leader' as const },
      ...Array.from({ length: 30 }, (_, i) => ({ id: `m${i}`, role: 'member' as const })),
      { id: 'a', role: 'auditor' as const },
    ]
    const l = layoutRoom({ agents })
    expect(l.depth).toBeGreaterThan(15)
    const rects = l.desks.map((d) => ({ x: d.x, z: d.z, w: 2.6, d: 1.2 }))
    for (let i = 0; i < rects.length; i++)
      for (let j = i + 1; j < rects.length; j++) expect(overlap(rects[i], rects[j])).toBe(false)
    const aud = l.desks.find((d) => d.id === 'a')!
    for (const m of l.desks.filter((d) => d.id.startsWith('m'))) expect(aud.z).toBeGreaterThan(m.z - 1)
  })
})

describe('placeRooms', () => {
  it('keeps room footprints apart and off the building', () => {
    const rooms = placeRooms(5)
    expect(rooms).toHaveLength(5)
    const fp = rooms.map((r) => ({ x: r.x, z: r.z, w: 19, d: 15 }))
    for (let i = 0; i < fp.length; i++)
      for (let j = i + 1; j < fp.length; j++) expect(overlap(fp[i], fp[j])).toBe(false)
    const building = { x: 2.5, z: -4, w: 35, d: 28 }
    for (const f of fp) expect(overlap(f, building)).toBe(false)
  })
})

describe('layoutConference', () => {
  const noSeatOverlap = (seats: { x: number; z: number }[]) => {
    const rects = seats.map((s) => ({ ...s, w: 0.8, d: 0.8 }))
    for (let i = 0; i < rects.length; i++)
      for (let j = i + 1; j < rects.length; j++) expect(overlap(rects[i], rects[j])).toBe(false)
  }

  it('matches the mockup table for a few people', () => {
    const c = layoutConference(3)
    expect(c.depth).toBe(15)
    expect(c.seats).toHaveLength(10)
    expect(c.seats[0].x).toBeCloseTo(5.1)
    expect(c.seats[0].z).toBeCloseTo(6)
    expect(c.tables).toHaveLength(1)
    expect(c.tables[0].length).toBeCloseTo(11)
  })

  it('lengthens the one table up to seven a side', () => {
    const c = layoutConference(14)
    expect(c.depth).toBe(15)
    expect(c.seats).toHaveLength(14)
    expect(c.tables).toHaveLength(1)
    expect(c.tables[0].length).toBeCloseTo(15.4)
    for (const s of c.seats) expect(s.x - 0.4).toBeGreaterThan(0)
    noSeatOverlap(c.seats)
  })

  it('adds a second table row and depth beyond fourteen', () => {
    const c = layoutConference(20)
    expect(c.depth).toBeCloseTo(22.5)
    expect(c.seats.length).toBeGreaterThanOrEqual(20)
    expect(c.tables).toHaveLength(2)
    expect(c.tables[1].z - c.tables[0].z).toBeCloseTo(7.5)
    const xs = (zs: number[]) => c.seats.filter((q) => zs.some((z) => Math.abs(q.z - z) < 0.01)).map((q) => q.x)
    expect(xs([13.5, 18.5]).every((x) => xs([6, 11]).some((y) => Math.abs(x - y) < 0.01))).toBe(true)
    noSeatOverlap(c.seats)
  })
})

describe('fitFontSize', () => {
  const fit = (text: string) =>
    fitFontSize(text, (s) => s * text.length * 0.5, 9, 2.05, 0.95, 0.94)
  it('fits the text inside the board', () => {
    const s = fit('Experimentation')
    expect(s * 'Experimentation'.length * 0.5).toBeLessThanOrEqual(9 * 0.94 + 1e-9)
    expect(s).toBeLessThanOrEqual(2.05 * 0.95)
  })
  it('gives shorter text a larger size', () => {
    expect(fit('Grounding')).toBeGreaterThan(fit('Experimentation'))
  })
})

describe('toPreviewChart', () => {
  const sizes = { grounding: 4, hypotheses: 4, experimentation: 5, validation: 5 } as const
  const lab: Laboratory = {
    id: 'built-in',
    name: 'Built-in laboratory',
    rooms: (Object.keys(sizes) as (keyof typeof sizes)[]).map((stage) => ({
      stage,
      team: stage,
      agents: Array.from({ length: sizes[stage] }, (_, i) => ({
        name: i === 0 ? 'leader' : i === sizes[stage] - 1 ? 'auditor' : `m${i}`,
        role: i === 0 ? ('leader' as const) : i === sizes[stage] - 1 ? ('auditor' as const) : ('member' as const),
      })),
    })),
  }
  it('shows every agent idle with only the signed-in person and no ticker', () => {
    const c = toPreviewChart(lab, { user_id: 'u1', name: 'Ada' })
    const agents = c.rooms.flatMap((r) => r.agents)
    expect(c.rooms).toHaveLength(4)
    expect(agents).toHaveLength(18)
    expect(agents.every((a) => a.status === 'idle' && a.studies.length === 0 && a.source === 'laboratory')).toBe(true)
    expect(c.people.map((p) => p.user_id)).toEqual(['u1'])
    expect(c.ticker).toBeNull()
    expect(c.rooms[0]!.agents.map((a) => a.reports_to)).toEqual([
      'the researchers', 'Grounding leader', 'Grounding leader', 'nobody: checks Grounding independently',
    ])
    expect(new Set(agents.map((a) => a.id)).size).toBe(18)
  })
})
