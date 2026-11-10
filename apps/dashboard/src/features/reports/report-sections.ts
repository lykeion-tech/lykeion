import type { ReportEdge, ReportNode, ReviewCheck, StudyReport } from '../../api/types';
import { checkLabel } from '../../lib/review';
import { columnName } from '../../lib/studies';

// A section shows only what the report recorded: every label, status and
// detail below is copied from a node, edge, check or steering entry.
export type DetailBlock = { label: string; text: string };
export type Row =
  | {
      kind: 'detail';
      label: string;
      status?: 'pass' | 'unclear' | 'fail';
      pct?: number;
      detail: DetailBlock[];
    }
  | { kind: 'link'; nodeId: string; label: string };
// srSummary, when set, is the summary in words for screen readers.
export type Fold = { title: string; summary: string; srSummary?: string; rows: Row[] };
export type Status = { icon: '✓' | '◐' | '○' | ''; word: string; srText: string };

const statuses: Record<string, [Status['icon'], string]> = {
  supported: ['✓', 'Supported'],
  caveated: ['◐', 'Supported with caveats'],
  refuted: ['○', 'Refuted'],
  inconclusive: ['◐', 'Inconclusive'],
  invalid: ['○', 'Invalid'],
  kept: ['✓', 'Kept'],
  rejected: ['○', 'Rejected'],
  pass: ['✓', 'Supported'],
  unclear: ['◐', 'Unclear'],
  fail: ['○', 'Unsupported'],
};

export function statusOf(status: string): Status {
  const [icon, word] = statuses[status] ?? ['', ''];
  return { icon, word, srText: word };
}

export const kindName = (kind: string) => kind.charAt(0).toUpperCase() + kind.slice(1);

const pct = (p: number) => Math.round(p * 100);
const reviewStatus = (s: string) =>
  s === 'pass' || s === 'unclear' || s === 'fail' ? s : undefined;

function checkRow(check: ReviewCheck): Row {
  const detail: DetailBlock[] = [];
  if (check.by === 'jev') {
    if (check.question) detail.push({ label: 'Jev asked', text: check.question });
    if (check.p !== undefined) detail.push({ label: 'Evidence', text: `${pct(check.p)}%` });
  } else {
    detail.push({ label: 'Checked by the server', text: check.detail || checkLabel(check.id) });
  }
  if (check.warning) detail.push({ label: 'Warning only', text: 'Counts toward nothing.' });
  return {
    kind: 'detail',
    label: checkLabel(check.id),
    status: reviewStatus(check.status),
    pct: check.p === undefined ? undefined : pct(check.p),
    detail,
  };
}

const reasonRow = (reason: string, node: ReportNode): Row => ({
  kind: 'detail',
  label: reason,
  detail: node.column ? [{ label: 'Recorded in', text: columnName(node.column) }] : [],
});

const count = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

export function sectionFor(report: StudyReport, nodeId: string) {
  const byId = new Map(report.nodes.map((n) => [n.id, n]));
  const node = byId.get(nodeId)!;
  const linkTo = (id: string): Row[] => {
    const n = byId.get(id);
    return n ? [{ kind: 'link', nodeId: n.id, label: n.label }] : [];
  };
  const unique = (ids: string[]) => [...new Set(ids)];
  const out = (from: string, ...kinds: ReportEdge['kind'][]) =>
    report.edges.filter((e) => e.from === from && kinds.includes(e.kind)).map((e) => e.to);
  const into = (to: string, ...kinds: ReportEdge['kind'][]) =>
    report.edges.filter((e) => e.to === to && kinds.includes(e.kind)).map((e) => e.from);
  const links = (ids: string[]) => unique(ids).flatMap(linkTo);
  const linkFold = (title: string, ids: string[]): Fold => {
    const rows = links(ids);
    return { title, summary: rows.length ? String(rows.length) : 'None recorded', rows };
  };

  const runRows = (): Row[] =>
    node.run_id
      ? [
          {
            kind: 'detail',
            label: node.agent || 'Agent run',
            detail: [
              ...(node.agent ? [{ label: 'Agent', text: node.agent }] : []),
              { label: 'Run', text: node.run_id },
              ...(node.column ? [{ label: 'Column', text: columnName(node.column) }] : []),
            ],
          },
        ]
      : [];
  const steeringRows = (): Row[] =>
    report.steering.map((s) => ({
      kind: 'detail',
      // The server links steering to the study, not to any node.
      label: `Study steering · ${s.text}`,
      detail: [
        {
          label: `${s.kind === 'comment' ? "A researcher's comment" : 'An answered decision'} on the study, not linked to this node`,
          text: new Date(s.at).toLocaleString(),
        },
      ],
    }));
  const whoFold = (title: string, judged?: string): Fold => {
    const rows = [
      ...runRows(),
      ...steeringRows(),
      ...(judged && node.checks.length
        ? [
            {
              kind: 'detail' as const,
              label: `Judged in ${judged}`,
              detail: [{ label: 'Checks recorded', text: String(node.checks.length) }],
            },
          ]
        : []),
    ];
    return { title, summary: rows.length ? String(rows.length) : 'None recorded', rows };
  };

  const folds: Fold[] = [];
  switch (node.kind) {
    case 'finding': {
      if (node.status === 'inconclusive' || node.status === 'invalid') {
        folds.push({
          title: node.status === 'inconclusive' ? 'Why inconclusive' : 'Why invalid',
          summary: node.reasons.length ? count(node.reasons.length, 'reason') : 'None recorded',
          rows: node.reasons.map((r) => reasonRow(r, node)),
        });
      }
      const by = (s: string) => node.checks.filter((c) => c.status === s).length;
      folds.push({
        title: 'Checks · evidence',
        summary: `✓ ${by('pass')} · ◐ ${by('unclear')} · ○ ${by('fail')}`,
        srSummary: `${by('pass')} supported, ${by('unclear')} unclear, ${by('fail')} unsupported`,
        rows: node.checks.map(checkRow),
      });
      const challengers = links(into(node.id, 'challenges'));
      const rerun: Row[] =
        node.rerun === undefined || node.rerun === null
          ? []
          : [
              {
                kind: 'detail',
                label: 'Validation rerun',
                detail: [{ label: 'Rerun as recorded', text: JSON.stringify(node.rerun, null, 2) }],
              },
            ];
      folds.push({
        title: 'Rerun · objections',
        summary: `${rerun.length ? 'Rerun' : 'No rerun'} · ${count(challengers.length, 'objection or doubt', 'objections and doubts')}`,
        rows: [...rerun, ...challengers],
      });
      const hypotheses = into(node.id, 'tests');
      folds.push(
        linkFold('Sources', [
          ...hypotheses.flatMap((h) =>
            into(h, 'grounds').filter((id) =>
              ['paper', 'dataset'].includes(byId.get(id)?.kind ?? ''),
            ),
          ),
          ...into(node.id, 'supports').filter((id) => byId.get(id)?.kind === 'output'),
        ]),
      );
      folds.push(whoFold('Who decided', 'Validation'));
      break;
    }
    case 'hypothesis':
      if (node.status === 'rejected') {
        const rows = [
          ...node.reasons.map((r) => reasonRow(r, node)),
          ...node.checks.filter((c) => c.status === 'fail').map(checkRow),
        ];
        folds.push({
          title: 'Why rejected',
          summary: rows.length ? String(rows.length) : 'None recorded',
          rows,
        });
      }
      folds.push(linkFold('Tested by', out(node.id, 'tested_by', 'tests')));
      folds.push(linkFold('Grounded in', into(node.id, 'grounds')));
      folds.push(whoFold('Who decided', 'Hypotheses'));
      break;
    case 'paper':
    case 'dataset':
      folds.push(linkFold('Cited by', out(node.id, 'grounds')));
      folds.push(whoFold('Who found it'));
      break;
    case 'experiment':
      folds.push(linkFold('Outputs', out(node.id, 'produced')));
      folds.push(whoFold('Who decided'));
      break;
    case 'output':
      folds.push(linkFold('Supports', out(node.id, 'supports')));
      folds.push(whoFold('Who made it'));
      break;
    case 'objection':
    case 'doubt': {
      folds.push(linkFold('Affects', out(node.id, 'challenges')));
      const raised =
        node.kind === 'doubt'
          ? node.checks.filter((c) => c.status === 'unclear').map(checkRow)
          : runRows();
      folds.push({
        title: 'Raised by',
        summary:
          node.kind === 'doubt' && node.column
            ? `Jev · ${columnName(node.column)}`
            : node.agent || '',
        rows: raised,
      });
      break;
    }
    case 'question':
      folds.push(linkFold('Hypotheses', out(node.id, 'tests')));
      break;
  }
  return { node, status: statusOf(node.status), folds };
}

export function overviewOf(report: StudyReport) {
  const n = (kind: string, status?: string) =>
    report.nodes.filter((x) => x.kind === kind && (status === undefined || x.status === status))
      .length;
  return {
    supported: n('finding', 'supported'),
    caveated: n('finding', 'caveated'),
    refuted: n('finding', 'refuted'),
    inconclusive: n('finding', 'inconclusive'),
    invalid: n('finding', 'invalid'),
    rejected: n('hypothesis', 'rejected'),
    objections: n('objection'),
    doubts: n('doubt'),
  };
}
