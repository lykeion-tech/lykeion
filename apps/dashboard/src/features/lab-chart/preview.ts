import type { LabChart, Laboratory } from '../../api/types';
import { STAGE_LABEL } from './scene';

const reportsTo = (role: 'leader' | 'member' | 'auditor', stage: keyof typeof STAGE_LABEL) =>
  role === 'leader'
    ? 'the researchers'
    : role === 'member'
      ? `${STAGE_LABEL[stage]} leader`
      : `nobody: checks ${STAGE_LABEL[stage]} independently`;

// A laboratory seen before any study uses it: every agent idle, nobody but the viewer present.
export function toPreviewChart(lab: Laboratory, me: { user_id: string; name: string }): LabChart {
  return {
    rooms: lab.rooms.map((room) => ({
      stage: room.stage,
      team: room.team,
      agents: room.agents.map((a) => ({
        id: `${room.stage}:${a.name}`,
        name: a.name,
        emoji: '',
        role: a.role,
        source: 'laboratory' as const,
        status: 'idle' as const,
        since: null,
        studies: [],
        machine: null,
        cli: null,
        reports_to: reportsTo(a.role, room.stage),
      })),
    })),
    people: [{ user_id: me.user_id, name: me.name, role: 'owner', open_decisions: 0 }],
    ticker: null,
  };
}
