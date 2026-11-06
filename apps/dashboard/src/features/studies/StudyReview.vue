<script setup lang="ts">
import { computed, h, ref, watch } from 'vue';
import { useCard, useCardReview, useKindRecords } from '../../api/queries';
import { workspaceId } from '../../api/session';
import type { CheckStatus, Column, ReviewAttempt } from '../../api/types';
import {
  checkLabel,
  defaultSpoke,
  indexOf,
  legacyColumns,
  spokes,
  statusWord,
  tileOf,
} from '../../lib/review';
import { columnName } from '../../lib/studies';

const props = defineProps<{ cardId: string }>();
const card = useCard(() => props.cardId);
const review = useCardReview(() => props.cardId);
const stages: Column[] = ['grounding', 'hypotheses', 'experimentation', 'validation'];
const byTime = (a: ReviewAttempt, b: ReviewAttempt) => Date.parse(a.at) - Date.parse(b.at);
const columns = computed(() =>
  stages.map((column) => ({
    ...review.data.value?.find((c) => c.column === column),
    column,
    attempts: [...(review.data.value?.find((c) => c.column === column)?.attempts ?? [])].sort(
      byTime,
    ),
  })),
);
const newest = computed(() =>
  columns.value
    .flatMap((c) => c.attempts)
    .sort(byTime)
    .at(-1),
);
const shown = ref<Column>('grounding');
const manuallyShown = ref(false);
watch(
  [workspaceId, () => props.cardId],
  () => {
    manuallyShown.value = false;
  },
  { flush: 'sync' },
);
watch(
  [workspaceId, () => props.cardId, newest, () => card.data.value?.board_column],
  () => {
    if (manuallyShown.value) return;
    shown.value = newest.value?.column ?? card.data.value?.board_column ?? 'grounding';
  },
  { immediate: true },
);
function showColumn(column: Column) {
  manuallyShown.value = true;
  shown.value = column;
}
const attempt = computed(() =>
  columns.value.find((c) => c.column === shown.value)?.attempts.at(-1),
);
const items = computed(() =>
  attempt.value ? [...attempt.value.items, ...(attempt.value.missed ?? [])] : [],
);
const checks = computed(() => (attempt.value ? spokes(attempt.value) : []));
const index = computed(() =>
  indexOf(checks.value.map((s) => items.value[s.item]!.checks[s.check]!)),
);
const picked = ref<string | null>(null);
watch(
  attempt,
  (a) => {
    picked.value = a ? defaultSpoke(a) : null;
  },
  { immediate: true },
);
const selected = computed(() => {
  const spoke = checks.value.find((s) => s.id === picked.value);
  if (!spoke) return null;
  const item = items.value[spoke.item]!;
  return { spoke, item, check: item.checks[spoke.check]! };
});
const colors: Record<CheckStatus, string> = {
  pass: '#278241',
  unclear: '#c69c25',
  fail: '#d5213d',
  withdrawn: 'var(--muted)',
};
const pct = (p: number) => Math.round(p * 100);
const point = (angle: number, radius: number) => ({
  x: 215 + Math.cos(angle) * radius,
  y: 165 + Math.sin(angle) * radius,
});
const angledSpokes = computed(() => {
  const groups = [...new Set(checks.value.map((s) => s.item))];
  const gap = Math.min(0.22, Math.PI / Math.max(1, groups.length));
  const per = (2 * Math.PI - groups.length * gap) / Math.max(1, checks.value.length);
  return checks.value.map((spoke, i) => {
    const group = groups.indexOf(spoke.item);
    const angle = -Math.PI / 2 + gap / 2 + group * gap + (i + 0.5) * per;
    return {
      ...spoke,
      angle,
      label: `${items.value[spoke.item]!.label} · ${checkLabel(items.value[spoke.item]!.checks[spoke.check]!.id)} · ${pct(spoke.p)}%`,
    };
  });
});
const labelGroups = computed(() =>
  [...new Set(angledSpokes.value.map((s) => s.item))].map((item) => {
    const group = angledSpokes.value.filter((s) => s.item === item);
    const angle = (group[0]!.angle + group.at(-1)!.angle) / 2;
    return { item, angle, right: Math.cos(angle) >= 0 };
  }),
);
const chart = computed(() => {
  const right = labelGroups.value.filter((g) => g.right).length;
  return Math.max(right, labelGroups.value.length - right) > 3
    ? { inner: 44, outer: 88 }
    : { inner: 58, outer: 128 };
});
const fingerprint = computed(() =>
  angledSpokes.value.map((spoke) => ({
    ...spoke,
    start: point(spoke.angle, chart.value.inner + 4),
    end: point(
      spoke.angle,
      chart.value.inner + 4 + (chart.value.outer - chart.value.inner - 4) * spoke.p,
    ),
    outer: point(spoke.angle, chart.value.outer),
  })),
);
const tiles = computed(() =>
  columns.value.map((c) => ({
    column: c.column,
    ...tileOf(c, card.data.value?.board_column ?? shown.value),
  })),
);

const loadLegacy = computed(() => review.isSuccess.value && !newest.value);
const scores = useKindRecords(() => props.cardId, 'score', loadLegacy);
const outcomes = useKindRecords(() => props.cardId, 'outcome', loadLegacy);
const historyError = computed(() => scores.error.value ?? outcomes.error.value);
function retryHistory() {
  if (scores.isError.value) void scores.refetch();
  if (outcomes.isError.value) void outcomes.refetch();
}
const legacy = computed(() =>
  loadLegacy.value && outcomes.data.value?.length
    ? legacyColumns(scores.data.value ?? [], outcomes.data.value)
    : [],
);
const checksOpen = ref(false);
const checkFilter = ref<CheckStatus | 'all'>('all');
const filters = [
  { status: 'all', label: 'All' },
  { status: 'fail', label: 'Unsupported' },
  { status: 'unclear', label: 'Unclear' },
  { status: 'pass', label: 'Supported' },
] as const;
const count = (status: CheckStatus | 'all') =>
  checks.value.filter((check) => status === 'all' || check.status === status).length;
const checkGroups = computed(() =>
  items.value.flatMap((item, i) => {
    const rows = checks.value.filter(
      (spoke) =>
        spoke.item === i && (checkFilter.value === 'all' || spoke.status === checkFilter.value),
    );
    return rows.length ? [{ item, index: i, rows }] : [];
  }),
);
const icons: Record<CheckStatus, string> = { pass: '✓', unclear: '◐', fail: '○', withdrawn: '—' };
const serverChecks = computed(() =>
  items.value.flatMap((item) =>
    item.checks.filter((check) => check.by === 'server').map((check) => ({ item, check })),
  ),
);
const serverFamilies = computed(() =>
  [...new Set(serverChecks.value.map(({ check }) => checkLabel(check.id)))].map((label) => ({
    label,
    rows: serverChecks.value.filter(({ check }) => checkLabel(check.id) === label),
  })),
);
const serverOpen = ref(false);
watch(
  attempt,
  () => {
    checksOpen.value = false;
    checkFilter.value = 'all';
    serverOpen.value =
      shown.value === 'experimentation' ||
      serverChecks.value.some(({ check }) => check.status === 'fail');
  },
  { immediate: true },
);
const ServerChecks = () =>
  h('section', { class: 'server-checks review-fold' }, [
    h(
      'button',
      {
        class: 'fold-toggle',
        'aria-label': 'Checked by the server',
        'aria-expanded': serverOpen.value,
        onClick: () => {
          serverOpen.value = !serverOpen.value;
        },
      },
      [
        h('b', 'Checked by the server'),
        h(
          'span',
          { class: 'type-caption' },
          `${serverChecks.value.filter(({ check }) => check.status === 'pass').length} passed · ${serverChecks.value.filter(({ check }) => check.status === 'fail').length} failed`,
        ),
      ],
    ),
    ...(serverOpen.value
      ? [
          ...serverFamilies.value.map((family) =>
            h('section', { class: 'server-family', key: family.label }, [
              h('h3', { class: 'type-caption' }, family.label),
              ...family.rows.map(({ item, check }, i) =>
                h('div', { class: 'server-row', key: `${item.key}-${check.id}-${i}` }, [
                  h(
                    'span',
                    {
                      style: { color: colors[check.status] },
                      'aria-label': statusWord(check.status),
                    },
                    icons[check.status],
                  ),
                  h('div', [h('b', item.label), h('p', check.detail ?? '')]),
                ]),
              ),
            ]),
          ),
          ...(shown.value === 'grounding' && attempt.value?.recall
            ? [
                h(
                  'p',
                  { class: 'type-caption recall' },
                  `Searched “${attempt.value.recall.query}” · ${attempt.value.recall.hits} found · ${attempt.value.recall.screened} screened by Jev`,
                ),
              ]
            : []),
        ]
      : []),
  ]);
const validation = computed(() => columns.value.find((column) => column.column === 'validation'));
const objector = computed(() => validation.value?.objector ?? attempt.value?.objector);
const upheldObjections = computed(() =>
  (validation.value?.objections ?? []).filter((objection) => objection.upheld === true),
);
function citesObjection(status: Record<string, unknown>, objection: Record<string, unknown>) {
  // Final statuses cite the judged objection's record, not its target.
  // Target matching is only for older responses without record keys.
  return (
    Array.isArray(status.cites) && status.cites.includes(objection.key || objection.target_key)
  );
}
const objectionText = (objection: Record<string, unknown>) =>
  `${String(objection.type ?? '').replaceAll('_', ' ')}: ${String(objection.problem ?? '')}`;
const validationStatuses = computed(() =>
  (validation.value?.statuses ?? []).map((status) => ({
    hypothesis: String(status.hypothesis ?? '').slice(0, 80),
    status: String(status.status ?? ''),
    reason: Array.isArray(status.reasons) ? String(status.reasons[0] ?? '') : '',
    objections: upheldObjections.value
      .filter((objection) => citesObjection(status, objection))
      .map(objectionText),
  })),
);
const unlinkedObjections = computed(() =>
  upheldObjections.value
    .filter(
      (objection) =>
        !(validation.value?.statuses ?? []).some((status) => citesObjection(status, objection)),
    )
    .map(objectionText),
);
</script>

<template>
  <section class="study-review surface-content type-body" aria-label="Study review">
    <h2 class="review-heading">Review</h2>
    <p v-if="review.isPending.value" class="review-empty" role="status">Loading review…</p>
    <p v-else-if="review.isError.value" class="inline-error" role="alert">
      {{ review.error.value?.message }}
      <button class="text-button" :disabled="review.isFetching.value" @click="review.refetch()">
        Retry
      </button>
    </p>
    <p v-else-if="loadLegacy && historyError" class="inline-error" role="alert">
      {{ historyError.message }}
      <button class="text-button" @click="retryHistory">Retry</button>
    </p>
    <p
      v-else-if="loadLegacy && (scores.isPending.value || outcomes.isPending.value)"
      class="review-empty"
      role="status"
    >
      Loading review history…
    </p>
    <section v-else-if="legacy.length" class="legacy-review" aria-label="Legacy review">
      <h2>Reviewed under the old scoring</h2>
      <section v-for="column in legacy" :key="column.column">
        <h3>{{ columnName(column.column) }} · {{ column.verdict }}</h3>
        <p v-for="(criterion, i) in column.criteria" :key="i">
          {{ criterion.name }} — {{ criterion.level }}
        </p>
      </section>
    </section>
    <template v-else>
      <div class="fingerprint-panel">
        <p v-if="attempt && shown === 'experimentation'" class="warning-caption type-caption">
          Experimentation · warnings only
        </p>
        <p v-if="!newest" class="review-empty">
          Jev reviews each column when its agents finish. Nothing to review yet.
        </p>
        <p v-else-if="!attempt" class="review-empty">
          {{ card.data.value?.board_column === shown ? 'being worked on' : 'not reached' }}
        </p>
        <p v-else-if="!checks.length" class="review-empty">
          <template v-if="shown === 'validation' && validation?.mode === 'runs'">
            Hypothesis statuses were determined from recorded runs.
          </template>
          <template
            v-else-if="
              ['redo', 'block'].includes(attempt.decision) &&
              serverChecks.some(({ check }) => check.status === 'fail')
            "
          >
            No Jev checks this try: the server's checks stopped it.
          </template>
          <template v-else>No Jev checks this try.</template>
        </p>
        <svg
          v-else
          class="fingerprint"
          width="100%"
          viewBox="0 0 430 330"
          aria-label="Claim fingerprint"
        >
          <g v-for="ring in [0, 0.35, 0.7, 1]" :key="ring">
            <circle
              cx="215"
              cy="165"
              :r="chart.inner + ring * (chart.outer - chart.inner)"
              fill="none"
              stroke="var(--border)"
              :stroke-dasharray="ring ? '2 4' : undefined"
            />
          </g>
          <g v-for="spoke in fingerprint" :key="spoke.id">
            <line
              :x1="spoke.start.x"
              :y1="spoke.start.y"
              :x2="spoke.outer.x"
              :y2="spoke.outer.y"
              stroke="var(--border)"
              stroke-width="7"
              stroke-linecap="round"
            />
            <line
              :x1="spoke.start.x"
              :y1="spoke.start.y"
              :x2="spoke.end.x"
              :y2="spoke.end.y"
              :stroke="colors[spoke.status]"
              stroke-width="5"
              stroke-linecap="round"
              :stroke-dasharray="spoke.warning ? '4 4' : undefined"
            />
            <circle
              v-if="picked === spoke.id"
              :cx="spoke.end.x"
              :cy="spoke.end.y"
              r="5"
              fill="none"
              stroke="var(--text)"
              stroke-width="1.5"
            />
            <line
              class="spoke-hit"
              :x1="spoke.start.x"
              :y1="spoke.start.y"
              :x2="spoke.outer.x"
              :y2="spoke.outer.y"
              stroke="transparent"
              stroke-width="16"
              role="button"
              tabindex="0"
              :aria-label="spoke.label"
              :aria-pressed="picked === spoke.id"
              @click="picked = spoke.id"
              @keydown.enter="picked = spoke.id"
              @keydown.space.prevent="picked = spoke.id"
            />
          </g>
          <text
            x="215"
            y="173"
            text-anchor="middle"
            class="support-index"
            aria-label="Support index"
          >
            {{ index ?? '—' }}
          </text>
          <text x="215" y="191" text-anchor="middle" class="index-caption">Support index</text>
          <text x="215" y="204" text-anchor="middle" class="index-scale">out of 100</text>
        </svg>
        <div v-if="checks.length" class="legend type-caption">
          <span><i :style="{ color: colors.pass }">✓</i> Supported</span>
          <span><i :style="{ color: colors.unclear }">◐</i> Unclear</span>
          <span><i :style="{ color: colors.fail }">○</i> Unsupported</span>
        </div>
        <p v-if="checks.length" class="fingerprint-note type-caption">
          spoke length = evidence the claim holds
        </p>
        <section v-if="selected && attempt" class="selected-check" aria-label="Selected check">
          <div class="selected-title">
            <span class="type-caption">SELECTED</span
            ><b>{{ selected.item.label }}</b>
          </div>
          <div class="selected-status">
            <b :style="{ color: colors[selected.check.status] }">{{
              statusWord(selected.check.status)
            }}</b
            ><span class="type-caption">evidence {{ pct(selected.spoke.p) }}%</span>
          </div>
          <p class="check-question">{{ selected.check.question }}</p>
          <blockquote :style="{ borderLeftColor: colors[selected.check.status] }">
            {{ selected.item.quote }}
          </blockquote>
        </section>
        <div class="stage-tiles" aria-label="Review stages">
          <button
            v-for="tile in tiles"
            :key="tile.column"
            class="stage-tile"
            :aria-label="`Show ${columnName(tile.column)} review`"
            :aria-pressed="shown === tile.column"
            @click="showColumn(tile.column)"
          >
            <span class="tile-heading"
              ><span>{{ columnName(tile.column) }}</span
              ><b>{{ tile.index ?? '—' }}</b></span
            >
            <span class="tile-bar"><i :style="{ width: `${tile.index ?? 0}%` }" /></span>
            <small class="type-caption">{{
              tile.state === 'reviewed'
                ? `${tile.statusSummary ?? `${tile.pass} of ${tile.total} items pass`} · try ${tile.tryN}`
                : tile.state === 'working'
                  ? 'being worked on'
                  : 'not reached'
            }}</small>
          </button>
        </div>
        <section v-if="attempt" class="review-fold evidence-checks">
          <button
            class="fold-toggle"
            aria-label="Checks · evidence"
            :aria-expanded="checksOpen"
            @click="checksOpen = !checksOpen"
          >
            <b>Checks · evidence</b>
            <span class="type-caption"
              >✓ {{ count('pass') }} · ◐ {{ count('unclear') }} · ○ {{ count('fail') }}</span
            >
          </button>
          <template v-if="checksOpen">
            <div class="check-filters" aria-label="Filter checks">
              <button
                v-for="filter in filters"
                :key="filter.status"
                :aria-label="`${filter.label} checks`"
                :aria-pressed="checkFilter === filter.status"
                @click="checkFilter = filter.status"
              >
                {{ filter.label }} {{ count(filter.status) }}
              </button>
            </div>
            <section v-for="group in checkGroups" :key="group.index" class="check-group">
              <header class="check-group-heading">
                <b>{{ group.item.label }}</b>
                <span v-if="group.item.carried" class="review-tag">carried</span>
                <span v-if="attempt.weakest?.key === group.item.key" class="type-caption"
                  >weakest · Jev {{ pct(attempt.weakest.p) }}% sure</span
                >
                <p class="item-quote" :title="group.item.quote">{{ group.item.quote }}</p>
              </header>
              <button
                v-for="spoke in group.rows"
                :key="spoke.id"
                class="check-row"
                :aria-pressed="picked === spoke.id"
                @click="picked = spoke.id"
              >
                <span
                  :style="{ color: colors[spoke.status] }"
                  :aria-label="statusWord(spoke.status)"
                  >{{ icons[spoke.status] }}</span
                >
                <span class="check-row-label"
                  >{{ checkLabel(group.item.checks[spoke.check]!.id) }}
                  <span class="review-tag">{{ columnName(shown) }}</span
                  ><span v-if="spoke.warning" class="review-tag">warning only</span></span
                >
                <span class="evidence-bar" aria-hidden="true"
                  ><i :style="{ width: `${pct(spoke.p)}%`, background: colors[spoke.status] }" /><i
                    class="evidence-tick"
                    style="left: 35%" /><i class="evidence-tick" style="left: 70%"
                /></span>
                <span class="type-caption">{{ pct(spoke.p) }}%</span>
              </button>
            </section>
          </template>
        </section>
        <ServerChecks v-if="attempt" />
        <section v-if="shown === 'validation'" class="validation-details" aria-label="Validation">
          <h2>Validation</h2>
          <p v-if="objector?.ran === false">{{ objector.reason }}</p>
          <template v-if="validation?.mode === 'runs'">
            <section v-for="(status, i) in validationStatuses" :key="i" class="validation-status">
              <div>
                <b>{{ status.hypothesis }}</b> <span class="review-tag">{{ status.status }}</span>
              </div>
              <p>{{ status.reason }}</p>
              <p v-for="(objection, j) in status.objections" :key="j" class="validation-objection">
                {{ objection }}
              </p>
            </section>
          </template>
          <section
            v-if="unlinkedObjections.length"
            class="validation-unlinked"
            aria-label="Other upheld objections"
          >
            <h3 class="type-caption">Other upheld objections</h3>
            <p class="type-caption">
              These upheld objections have no recorded link to a hypothesis.
            </p>
            <p v-for="(objection, i) in unlinkedObjections" :key="i" class="validation-objection">
              {{ objection }}
            </p>
          </section>
          <p v-if="validation?.route_back">Sent back to Experimentation once</p>
        </section>
        <footer class="type-caption">
          Jev checks whether each claim is backed by the evidence this study recorded. It does not
          decide whether the science is true.
        </footer>
      </div>
    </template>
  </section>
</template>

<style scoped>
.study-review {
  min-width: 0;
  padding: 24px 20px;
}
.review-heading {
  margin: 0 0 8px;
  font-size: 16px;
}
.warning-caption {
  margin: 0 0 8px;
  color: var(--muted);
}
.fingerprint-panel {
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 14px;
}
.fingerprint {
  display: block;
  margin: 0 auto;
  max-width: 560px;
}
.support-index {
  font-size: 44px;
  font-weight: 700;
  fill: var(--text);
}
.index-caption {
  font-size: 10.5px;
  fill: var(--muted);
}
.index-scale {
  font-size: 9.5px;
  fill: var(--muted);
}
.spoke-hit {
  cursor: pointer;
}
.spoke-hit:focus {
  outline: none;
}
.spoke-hit:focus-visible {
  stroke: var(--text);
  stroke-opacity: 0.3;
}
.legend {
  display: flex;
  justify-content: center;
  flex-wrap: wrap;
  gap: 12px;
}
.legend i {
  font-style: normal;
}
.fingerprint-note {
  color: var(--muted);
  text-align: center;
  margin: 7px 0 18px;
}
.selected-check {
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 12px 14px;
  margin: 14px 0;
  overflow-wrap: anywhere;
}
.selected-title,
.selected-status {
  display: flex;
  align-items: baseline;
  gap: 9px;
  flex-wrap: wrap;
}
.selected-title > span,
.selected-status > span {
  color: var(--muted);
}
.selected-title b {
  font-weight: 500;
}
.selected-status {
  margin: 10px 0 8px;
}
.selected-status b {
  font-weight: 600;
}
.check-question {
  margin: 0 0 9px;
}
.selected-check blockquote {
  border-left: 2px solid;
  background: var(--input);
  padding: 9px 12px;
  margin: 0 0 9px;
  white-space: pre-wrap;
}
.stage-tiles {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 145px), 1fr));
  gap: 8px;
  margin-top: 18px;
}
.stage-tile {
  border: 1px solid var(--border);
  border-radius: 7px;
  padding: 10px;
  text-align: left;
  min-width: 0;
}
.stage-tile[aria-pressed='true'] {
  border-color: var(--muted);
  background: var(--hover);
}
.stage-tile:hover {
  background: var(--hover);
}
.tile-heading {
  display: flex;
  justify-content: space-between;
  gap: 6px;
  font-size: 12px;
}
.tile-heading b {
  font-variant-numeric: tabular-nums;
}
.tile-bar {
  display: block;
  height: 3px;
  background: var(--border);
  border-radius: 2px;
  margin: 10px 0 7px;
  overflow: hidden;
}
.tile-bar i {
  display: block;
  height: 100%;
  background: var(--muted);
}
.stage-tile small {
  display: block;
  color: var(--muted);
}
footer {
  border-top: 1px solid var(--border);
  margin-top: 16px;
  padding-top: 12px;
  color: var(--muted);
}
.review-empty {
  padding: 28px 12px;
  text-align: center;
  color: var(--muted);
}
.review-fold {
  margin-top: 16px;
  border-top: 1px solid var(--border);
  padding-top: 12px;
}
.fold-toggle,
.server-checks :deep(.fold-toggle) {
  display: flex;
  width: 100%;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  padding: 4px 0;
  text-align: left;
}
.fold-toggle > span,
.server-checks :deep(.fold-toggle > span) {
  color: var(--muted);
}
.check-filters {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin: 12px 0;
}
.check-filters button,
.review-tag {
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 2px 7px;
  font-size: 11px;
}
.check-filters button[aria-pressed='true'] {
  background: var(--hover);
  border-color: var(--muted);
}
.review-tag {
  display: inline-block;
  color: var(--muted);
  white-space: nowrap;
}
.check-group {
  margin: 15px 0;
}
.check-group-heading {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 4px 8px;
  min-width: 0;
}
.check-group-heading > .type-caption {
  color: var(--muted);
}
.item-quote {
  flex-basis: 100%;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--muted);
  margin: 2px 0 7px;
}
.check-row {
  display: grid;
  grid-template-columns: 16px minmax(0, 1fr) minmax(45px, 100px) 36px;
  gap: 8px;
  align-items: center;
  width: 100%;
  padding: 9px 4px;
  text-align: left;
  border-radius: 5px;
}
.check-row:hover,
.check-row[aria-pressed='true'] {
  background: var(--hover);
}
.check-row-label {
  overflow-wrap: anywhere;
}
.check-row-label .review-tag {
  margin-left: 4px;
}
.evidence-bar {
  display: block;
  position: relative;
  height: 5px;
  background: var(--border);
  border-radius: 3px;
}
.evidence-bar > i:first-child {
  display: block;
  height: 100%;
  border-radius: 3px;
}
.evidence-tick {
  position: absolute;
  height: 9px;
  width: 1px;
  top: -2px;
  background: var(--muted);
}
.server-checks :deep(.server-family),
.validation-status,
.validation-unlinked {
  border: 1px solid var(--border);
  border-radius: 7px;
  padding: 10px 12px;
  margin-top: 10px;
  overflow-wrap: anywhere;
}
.server-checks :deep(.server-family h3) {
  margin: 0 0 9px;
  color: var(--muted);
}
.server-checks :deep(.server-row) {
  display: flex;
  gap: 8px;
  margin: 8px 0;
}
.server-checks :deep(.server-row p),
.validation-status p,
.validation-unlinked p {
  margin: 4px 0 0;
}
.server-checks :deep(.server-row p),
.server-checks :deep(.recall) {
  color: var(--muted);
}
.server-checks :deep(.recall) {
  margin: 12px 0 0;
  line-height: 1.5;
  overflow-wrap: anywhere;
}
.validation-details {
  margin-top: 16px;
}
.validation-details h2,
.legacy-review h2 {
  font-size: 14px;
  font-weight: 600;
}
.legacy-review h3 {
  font-size: 13px;
  margin-top: 18px;
}
.validation-objection {
  padding-left: 12px;
  border-left: 2px solid var(--border);
}
</style>
