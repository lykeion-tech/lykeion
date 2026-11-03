<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useCard, useCardReview } from '../../api/queries';
import { workspaceId } from '../../api/session';
import type { CheckStatus, Column, ReviewAttempt } from '../../api/types';
import { checkLabel, defaultSpoke, indexOf, spokes, statusWord, tileOf } from '../../lib/review';
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
watch(
  [workspaceId, () => props.cardId, newest, () => card.data.value?.board_column],
  () => {
    shown.value = newest.value?.column ?? card.data.value?.board_column ?? 'grounding';
  },
  { immediate: true },
);
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
const fingerprint = computed(() => {
  const groups = [...new Set(checks.value.map((s) => s.item))];
  const gap = Math.min(0.22, Math.PI / Math.max(1, groups.length));
  const per = (2 * Math.PI - groups.length * gap) / Math.max(1, checks.value.length);
  return checks.value.map((spoke, i) => {
    const group = groups.indexOf(spoke.item);
    const angle = -Math.PI / 2 + gap / 2 + group * gap + (i + 0.5) * per;
    return {
      ...spoke,
      angle,
      start: point(angle, 62),
      end: point(angle, 62 + 66 * spoke.p),
      outer: point(angle, 128),
      label: `${items.value[spoke.item]!.label} · ${checkLabel(items.value[spoke.item]!.checks[spoke.check]!.id)} · ${pct(spoke.p)}%`,
    };
  });
});
const labels = computed(() =>
  [...new Set(fingerprint.value.map((s) => s.item))].map((i) => {
    const group = fingerprint.value.filter((s) => s.item === i);
    const angle = (group[0]!.angle + group.at(-1)!.angle) / 2;
    const item = items.value[i]!;
    return {
      item: i,
      ...point(angle, 148),
      anchor: Math.cos(angle) > 0.3 ? 'start' : Math.cos(angle) < -0.3 ? 'end' : 'middle',
      label: item.label.slice(0, 24),
      weakest: !!attempt.value?.weakest && attempt.value.weakest.key === item.key,
      index: indexOf(group.map((s) => item.checks[s.check]!)),
    };
  }),
);
const tiles = computed(() =>
  columns.value.map((c) => ({
    column: c.column,
    ...tileOf(c, card.data.value?.board_column ?? shown.value),
  })),
);
</script>

<template>
  <section class="study-review surface-content type-body" aria-label="Study review">
    <p v-if="review.isPending.value" class="review-empty" role="status">Loading review…</p>
    <p v-else-if="review.isError.value" class="inline-error" role="alert">
      {{ review.error.value?.message }}
      <button class="text-button" :disabled="review.isFetching.value" @click="review.refetch()">
        Retry
      </button>
    </p>
    <template v-else>
      <header class="fingerprint-caption type-caption" aria-label="Fingerprint caption">
        <span>Claim fingerprint</span>
        <span
          >{{ columnName(shown)
          }}<template v-if="attempt">
            · try {{ attempt.attempt }} · {{ checks.length }} checks<template
              v-if="shown === 'experimentation'"
            >
              · warnings only</template
            ></template
          ></span
        >
      </header>
      <div class="fingerprint-panel">
        <p v-if="!newest" class="review-empty">
          Jev reviews each column when its agents finish. Nothing to review yet.
        </p>
        <p v-else-if="!attempt" class="review-empty">
          {{ card.data.value?.board_column === shown ? 'being worked on' : 'not reached' }}
        </p>
        <p v-else-if="!checks.length" class="review-empty">
          No Jev checks this try: the server's checks stopped it.
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
              :r="58 + ring * 70"
              fill="none"
              stroke="var(--border)"
              :stroke-dasharray="ring ? '2 4' : undefined"
            />
            <text x="218" :y="105 - ring * 70" class="ring-label">{{ pct(ring) }}%</text>
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
          <g v-for="label in labels" :key="label.item">
            <text :x="label.x" :y="label.y" :text-anchor="label.anchor" class="item-label">
              {{ label.label }}{{ label.weakest ? ' · weakest' : '' }}
            </text>
            <text :x="label.x" :y="label.y + 14" :text-anchor="label.anchor" class="item-index">
              {{ label.index ?? '—' }}
            </text>
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
        <div v-if="attempt" class="legend type-caption">
          <span><i :style="{ color: colors.pass }">✓</i> Supported</span>
          <span><i :style="{ color: colors.unclear }">◐</i> Unclear</span>
          <span><i :style="{ color: colors.fail }">○</i> Unsupported</span>
        </div>
        <p v-if="attempt" class="fingerprint-note type-caption">
          spoke length = evidence the claim holds
        </p>
        <section v-if="selected && attempt" class="selected-check" aria-label="Selected check">
          <div class="selected-title">
            <span class="type-caption">SELECTED</span
            ><b>{{ selected.item.label }} · {{ checkLabel(selected.check.id) }}</b>
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
          <p class="type-caption">
            cites {{ selected.item.cites.join(', ') }} · {{ columnName(shown) }} · try
            {{ attempt.attempt }}
          </p>
        </section>
        <div class="stage-tiles" aria-label="Review stages">
          <button
            v-for="tile in tiles"
            :key="tile.column"
            class="stage-tile"
            :aria-label="`Show ${columnName(tile.column)} review`"
            :aria-pressed="shown === tile.column"
            @click="shown = tile.column"
          >
            <span class="tile-heading"
              ><span>{{ columnName(tile.column) }}</span
              ><b>{{ tile.index ?? '—' }}</b></span
            >
            <span class="tile-bar"><i :style="{ width: `${tile.index ?? 0}%` }" /></span>
            <small class="type-caption">{{
              tile.state === 'reviewed'
                ? `${tile.pass} of ${tile.total} items pass · try ${tile.tryN}`
                : tile.state === 'working'
                  ? 'being worked on'
                  : 'not reached'
            }}</small>
          </button>
        </div>
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
}
.fingerprint-caption {
  display: flex;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 6px 12px;
  margin-bottom: 8px;
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
  overflow: visible;
}
.ring-label {
  font-size: 8px;
  fill: var(--muted);
}
.item-label {
  font-size: 11.5px;
  fill: var(--text);
}
.item-index {
  font-size: 11.5px;
  font-weight: 700;
  fill: var(--text);
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
.spoke-hit:focus-visible {
  outline: none;
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
.selected-status > span,
.selected-check > .type-caption {
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
</style>
