import { describe, expect, it } from 'vitest'
import { layoutConference, layoutRoom, placeRooms } from './layout'
import { fitFontSize } from './text-fit'

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
  it('uses the base table for a few people', () => {
    const c = layoutConference(3)
    expect(c.depth).toBe(15)
    expect(c.seats).toHaveLength(10)
  })

  it('lengthens the table for a crowd without overlapping seats', () => {
    const c = layoutConference(14)
    expect(c.seats.length).toBeGreaterThanOrEqual(14)
    expect(c.depth).toBeGreaterThan(15)
    const rects = c.seats.map((s) => ({ ...s, w: 0.8, d: 0.8 }))
    for (let i = 0; i < rects.length; i++)
      for (let j = i + 1; j < rects.length; j++) expect(overlap(rects[i], rects[j])).toBe(false)
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
