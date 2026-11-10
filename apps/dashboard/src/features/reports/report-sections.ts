import type { ReportEdge, ReportLink, ReportNode, ReviewCheck, StudyReport } from '../../api/types';
import { checkLabel } from '../../lib/review';
import { columnName } from '../../lib/studies';

// A section shows only what the report recorded: every label, status and
// detail below is copied from a node, edge, check or steering entry.
// A block with a nodeId shows its text as a link to that node.
export type DetailBlock = { label: string; text: string; nodeId?: string };
export type Row =
  | {
      kind: 'detail';
      label: string;
      status?: 'pass' | 'unclear' | 'fail';
      pct?: number;
      // Replaces the bare "N%" beside the bar when set.
      pctText?: string;
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
  // An upheld objection is a problem Jev confirmed in the finding it targets.
  upheld: ['○', 'Upheld'],
  not_upheld: ['✓', 'Not upheld'],
};
// A doubt is a review item, not a claim: its pass or fail says nothing about support.
const doubtWords: Record<string, string> = { pass: 'Passed', fail: 'Failed' };

export function statusOf(status: string, kind?: string): Status {
  // A concept's status is its type, never a judgement.
  if (kind === 'concept') {
    const type = kindName(status.replaceAll('_', ' '));
    return { icon: '', word: type, srText: type };
  }
  const [icon, word] = statuses[status] ?? ['', ''];
  const w = (kind === 'doubt' && doubtWords[status]) || word;
  return { icon, word: w, srText: w };
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

const relationWords: Record<string, string> = {
  is_a: 'is a',
  part_of: 'is part of',
  associated_with: 'is associated with',
  increases: 'increases',
  decreases: 'decreases',
  measured_by: 'is measured by',
  mentioned_in: 'is mentioned in',
};
const choiceWords: Record<string, string> = {
  confirms: 'Confirms',
  contradicts: 'Contradicts',
  unrelated: 'Unrelated',
};
const noveltyWords = { new: 'New', confirms: 'Confirms', contradicts: 'Contradicts' };
// Mirrors the server's outputKind: only figures and tables are ever checked.
function checkable(contentType = ''): boolean {
  const mt = contentType.split(';')[0]!.trim().toLowerCase();
  return (
    mt.startsWith('image/') ||
    mt === 'application/pdf' ||
    mt === 'text/csv' ||
    mt === 'text/tab-separated-values'
  );
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
  const labelOf = (id: string) => byId.get(id)?.label ?? id;
  // A figure or table check names its output; Jev's check of an output for a
  // finding lives on the finding, under figure:<output> or table:<output>.
  const outputCheck = (finding: ReportNode, output: string) =>
    finding.checks.find((c) => c.id === `figure:${output}` || c.id === `table:${output}`);
  const outputRow = (check: ReviewCheck): Row => {
    const [kind, ...rest] = check.id.split(':');
    const output = rest.join(':');
    const row = checkRow(check);
    const detail: DetailBlock[] = [];
    if (kind === 'figure')
      detail.push({
        label: 'Jev judged',
        text:
          check.basis === 'image'
            ? 'Judged from the image.'
            : "Judged from the figure's recorded text, not the image.",
      });
    if (check.detail) detail.push({ label: 'Jev read', text: check.detail });
    return {
      ...row,
      label: `${kind === 'figure' ? 'Figure' : 'Table'} · ${labelOf(output)}`,
      detail: [...detail, ...(row.kind === 'detail' ? row.detail : [])],
    } as Row;
  };
  const notCheckedRow = (output: string): Row => ({
    kind: 'detail',
    label: `${labelOf(output)} · Not checked`,
    detail: [{ label: 'Jev', text: 'Not checked.' }],
  });
  // A figure or table is only called unchecked once Jev's checks are in;
  // other outputs are never checked.
  const uncheckedOutput = (finding: ReportNode, output: string) =>
    report.checks === 'ready' &&
    checkable(byId.get(output)?.content_type) &&
    !outputCheck(finding, output);
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
      const evidence = node.checks.filter((c) => !c.id.startsWith('novelty:'));
      const by = (s: string) => evidence.filter((c) => c.status === s).length;
      const isOutputCheck = (c: ReviewCheck) => /^(figure|table):/.test(c.id);
      const unchecked = unique(into(node.id, 'supports')).filter(
        (id) => byId.get(id)?.kind === 'output' && uncheckedOutput(node, id),
      );
      folds.push({
        title: 'Checks · evidence',
        summary: `✓ ${by('pass')} · ◐ ${by('unclear')} · ○ ${by('fail')}`,
        srSummary: `${by('pass')} supported, ${by('unclear')} unclear, ${by('fail')} unsupported`,
        rows: [
          ...evidence.filter((c) => !isOutputCheck(c)).map(checkRow),
          ...evidence.filter(isOutputCheck).map(outputRow),
          ...unchecked.map(notCheckedRow),
        ],
      });
      if (report.checks !== 'none') {
        const novelty = node.checks.filter((c) => c.id.startsWith('novelty:'));
        folds.push({
          title: 'Novelty',
          summary: node.novelty ? noveltyWords[node.novelty] : 'Not judged',
          rows: novelty.map((c) => {
            const paper = c.id.slice('novelty:'.length);
            const [first = '', ...abstract] = (c.detail ?? '').split('\n');
            const choice = first.replace(/^choice: /, '');
            // An unclear answer is shown, but never as settled.
            const word = `${choiceWords[choice] ?? choice}${c.status === 'unclear' ? ' (unclear)' : ''}`;
            const row = checkRow(c);
            return {
              ...row,
              label: `${labelOf(paper)} · ${word}`,
              detail: [
                {
                  label: 'Paper',
                  text: labelOf(paper),
                  ...(byId.has(paper) ? { nodeId: paper } : {}),
                },
                ...(row.kind === 'detail' ? row.detail : []),
                ...(abstract.length ? [{ label: 'Jev read', text: abstract.join('\n') }] : []),
              ],
            } as Row;
          }),
        });
      }
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
    case 'output': {
      const findings = unique(out(node.id, 'supports')).filter((id) => byId.has(id));
      const rows = findings.flatMap((id): Row[] => {
        const check = outputCheck(byId.get(id)!, node.id);
        return [
          ...linkTo(id),
          ...(check
            ? [outputRow(check)]
            : uncheckedOutput(byId.get(id)!, node.id)
              ? [notCheckedRow(node.id)]
              : []),
        ];
      });
      folds.push({
        title: 'Supports',
        summary: findings.length ? String(findings.length) : 'None recorded',
        rows,
      });
      folds.push(whoFold('Who made it'));
      break;
    }
    case 'concept': {
      const conceptLabel = (key: string) => labelOf(`concept:${key}`);
      folds.push({
        title: 'Facts about it',
        summary: node.facts?.length ? count(node.facts.length, 'fact') : 'None recorded',
        rows: (node.facts ?? []).map((f) => ({
          kind: 'detail',
          label: `${conceptLabel(f.subject)} ${relationWords[f.relation] ?? f.relation.replaceAll('_', ' ')} ${
            f.relation === 'mentioned_in' ? labelOf(f.object) : conceptLabel(f.object)
          }`,
          pct: pct(f.p),
          pctText: `evidence ${pct(f.p)}%`,
          detail: [
            { label: 'Quote', text: f.quote },
            {
              label: 'From',
              text: labelOf(f.source_node),
              ...(byId.has(f.source_node) ? { nodeId: f.source_node } : {}),
            },
          ],
        })),
      });
      break;
    }
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
  return { node, status: statusOf(node.status, node.kind), folds };
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

// The notices shown above the graph while the report's judged parts are
// missing.
export function noticesOf(report: StudyReport): string[] {
  const out: string[] = [];
  if (report.extras === 'pending')
    out.push(
      report.machine
        ? `Concepts and links are waiting for ${report.machine}.`
        : 'Concepts and links are being judged.',
    );
  if (report.checks === 'unavailable')
    out.push("Jev could not judge this report's figure, table and novelty checks.");
  return out;
}

// handInSegments cuts the hand-in at its links' byte offsets. The segments'
// texts join back to text exactly; a link that overlaps an earlier one, runs
// past the end or splits a character stays plain text.
export function handInSegments(
  text: string,
  links: ReportLink[],
): { text: string; node?: string }[] {
  const bytes = new TextEncoder().encode(text);
  // ignoreBOM keeps a leading byte-order mark as text instead of dropping it.
  const decoder = new TextDecoder('utf-8', { ignoreBOM: true });
  const decode = (from: number, to: number) => decoder.decode(bytes.subarray(from, to));
  // A UTF-8 continuation byte is 10xxxxxx; a character starts anywhere else.
  const boundary = (i: number) => i === bytes.length || (bytes[i]! & 0xc0) !== 0x80;
  const out: { text: string; node?: string }[] = [];
  let at = 0;
  for (const l of [...links].sort((a, b) => a.start - b.start)) {
    if (l.start < at || l.end <= l.start || l.end > bytes.length) continue;
    if (!boundary(l.start) || !boundary(l.end)) continue;
    if (l.start > at) out.push({ text: decode(at, l.start) });
    out.push({ text: decode(l.start, l.end), node: l.node });
    at = l.end;
  }
  if (at < bytes.length || !out.length) out.push({ text: decode(at, bytes.length) });
  return out;
}
