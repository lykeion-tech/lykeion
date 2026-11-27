import type { ReportEdge, ReportLink, ReportNode, ReviewCheck, StudyReport } from '../../api/types';
import { checkLabel } from '../../lib/review';
import { measurementText, objectFields } from '../../lib/measurement';
import { columnName, studyDisplayTitle } from '../../lib/studies';

// A section shows only what the report recorded: every label, status and
// detail below is copied from a node, edge, check or steering entry.
// A block with a nodeId shows its text as a link to that node.
export type DetailBlock = { label: string; text: string; nodeId?: string };
export type Row =
  | {
      kind: 'detail';
      inline?: boolean;
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

  const runRows = (inline = false): Row[] =>
    node.run_id
      ? [
          {
            kind: 'detail',
            inline,
            label: node.agent || 'Agent run',
            detail: [
              ...(node.agent ? [{ label: 'Agent', text: node.agent }] : []),
              inline
                ? { label: 'Study', text: studyDisplayTitle(report.title) }
                : { label: 'Run', text: node.run_id },
              ...(node.column
                ? [{ label: inline ? 'Stage' : 'Column', text: columnName(node.column) }]
                : []),
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
      ...runRows(title === 'Who found it'),
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
  const measurements = Array.isArray(node.measurements)
    ? node.measurements.flatMap((measurement): Row[] => {
        const fields = objectFields(measurement);
        const label = measurementText(fields?.measure, fields?.value);
        if (!fields || !label) return [];
        const { name, field, output } = fields;
        const source = typeof output === 'string' ? byId.get(`rec:${output}`) : undefined;
        return [
          {
            kind: 'detail',
            label,
            detail: [
              ...(typeof name === 'string' && name ? [{ label: 'Recorded as', text: name }] : []),
              ...(typeof field === 'string'
                ? [{ label: 'JSON field', text: field || '(root)' }]
                : []),
              ...(source?.kind === 'output' && source.id !== node.id
                ? [{ label: 'Verified output', text: source.label, nodeId: source.id }]
                : []),
            ],
          },
        ];
      })
    : [];
  if (measurements.length) {
    folds.push({
      title: 'Recorded measurements',
      summary: String(measurements.length),
      rows: measurements,
    });
  }
  switch (node.kind) {
    case 'finding': {
      if (node.estimate) {
        const e = node.estimate;
        const row = (label: string): Row => ({ kind: 'detail', label, detail: [] });
        folds.push({
          title: 'Estimate',
          summary: e.unit,
          rows: [
            row(`Method: ${e.method}`),
            row(`Unit: ${e.unit}`),
            ...(e.reported
              ? [row(`Published value: ${e.reported.value} (${e.reported.source})`)]
              : []),
          ],
        });
      }
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
  const status = statusOf(node.status, node.kind);
  // An estimate has no prediction to support: say what it is.
  if (node.estimate && node.status === 'supported') status.word = status.srText = 'Measured';
  return { node, status, folds };
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

// What the report reads as in one phrase, from its findings alone.
export function verdictOf(report: StudyReport): string {
  const o = overviewOf(report);
  const total = o.supported + o.caveated + o.refuted + o.inconclusive + o.invalid;
  if (!total) return 'No findings';
  if (o.supported === total) return 'Supported';
  if (o.supported + o.caveated === total) return 'Supported with caveats';
  if ((o.supported + o.caveated) * 2 > total) return 'Mostly supported';
  if ((o.refuted + o.invalid) * 2 >= total) return 'Not supported';
  return 'Mixed';
}

export type TabId = 'summary' | 'findings' | 'concerns' | 'sources';
export type ListRow = {
  id: string;
  kind: string;
  label: string;
  status: string;
  // The weakest Jev check's evidence, in percent.
  pct?: number;
  // Concerns: what the objection or doubt targets.
  on?: string;
  // Sources: how many findings lean on it.
  cited?: number;
};

// The nodes reachable from id in at most depth edges, either direction, id excluded.
function within(report: StudyReport, id: string, depth: number): Set<string> {
  const seen = new Set([id]);
  let frontier = new Set([id]);
  for (let d = 0; d < depth; d++) {
    const next = new Set<string>();
    for (const e of report.edges) {
      if (frontier.has(e.from) && !seen.has(e.to)) next.add(e.to);
      if (frontier.has(e.to) && !seen.has(e.from)) next.add(e.from);
    }
    next.forEach((n) => seen.add(n));
    frontier = next;
  }
  seen.delete(id);
  return seen;
}

// The rows of the Findings, Concerns and Sources tabs: the whole report, or
// only what lies near the focused node.
export function tabRows(
  report: StudyReport,
  focus: string | null,
): Record<Exclude<TabId, 'summary'>, ListRow[]> {
  const labelOf = (id: string) => report.nodes.find((n) => n.id === id)?.label ?? id;
  const near1 = focus ? within(report, focus, 1) : null;
  const near2 = focus ? within(report, focus, 2) : null;
  const pick = (kinds: string[], near: Set<string> | null, self: boolean) =>
    report.nodes.filter(
      (n) => kinds.includes(n.kind) && (!near || near.has(n.id) || (self && n.id === focus)),
    );
  const evidence = (n: ReportNode) => {
    const ps = n.checks
      .filter((c) => !c.id.startsWith('novelty:') && c.p !== undefined)
      .map((c) => c.p!);
    return ps.length ? Math.round(Math.min(...ps) * 100) : undefined;
  };
  const row = (n: ReportNode): ListRow => ({
    id: n.id,
    kind: n.kind,
    label: n.label,
    status: n.status,
    pct: evidence(n),
  });
  const rank = (n: ReportNode) => (n.kind === 'finding' ? 0 : 1);
  return {
    findings: pick(['finding', 'hypothesis'], near2, true)
      .sort((a, b) => rank(a) - rank(b))
      .map(row),
    concerns: pick(['objection', 'doubt'], near1, true).map((n) => ({
      ...row(n),
      on: report.edges
        .filter((e) => e.from === n.id && e.kind === 'challenges')
        .map((e) => labelOf(e.to))
        .join(', '),
    })),
    sources: pick(['paper', 'dataset', 'output'], near2, true).map((n) => ({
      ...row(n),
      cited: focus
        ? undefined
        : [...within(report, n.id, 2)].filter(
            (id) => report.nodes.find((x) => x.id === id)?.kind === 'finding',
          ).length,
    })),
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

// A hand-in link as the rendered hand-in can find it: its phrase, which
// occurrence of the phrase it is, and how many there are in the source, all
// counted without overlaps.
export interface HandInPhrase {
  phrase: string;
  occurrence: number;
  count: number;
  node: string;
}

// matches are where phrase's non-overlapping matches start in text.
function matches(text: string, phrase: string): number[] {
  const out: number[] = [];
  for (let at = text.indexOf(phrase); at >= 0; at = text.indexOf(phrase, at + phrase.length))
    out.push(at);
  return out;
}

// handInPhrases names each link of the hand-in source by its phrase and
// occurrence, so it can be found again once the hand-in is rendered.
export function handInPhrases(text: string, links: ReportLink[]): HandInPhrase[] {
  const out: HandInPhrase[] = [];
  let at = 0;
  for (const seg of handInSegments(text, links)) {
    if (seg.node && seg.text) {
      const all = matches(text, seg.text);
      const occurrence = all.indexOf(at);
      if (occurrence >= 0)
        out.push({ phrase: seg.text, occurrence, count: all.length, node: seg.node });
    }
    at += seg.text.length;
  }
  return out;
}

// placeLinks finds each phrase's occurrence in the rendered hand-in, given
// as its text nodes in order, and says where to wrap it: which text node and
// the offsets inside it. Only matches inside one linkable text node count.
// The Markdown syntax, link targets and escapes of the source can add or hide
// matches, so a phrase is wrapped only when the rendered text holds as many
// matches as the source does, and its own overlaps no other link; otherwise
// it stays plain, and the text is never changed.
export function placeLinks(
  texts: { text: string; linkable: boolean }[],
  phrases: HandInPhrase[],
): { index: number; start: number; end: number; node: string }[] {
  const out: { index: number; start: number; end: number; node: string }[] = [];
  for (const p of phrases) {
    const found = texts.flatMap((t, index) =>
      t.linkable ? matches(t.text, p.phrase).map((start) => ({ index, start })) : [],
    );
    const m = found[p.occurrence];
    if (found.length !== p.count || !m) continue;
    const end = m.start + p.phrase.length;
    if (out.some((o) => o.index === m.index && o.start < end && m.start < o.end)) continue;
    out.push({ index: m.index, start: m.start, end, node: p.node });
  }
  return out;
}
