<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useAnalytics, useResearches } from '../../api/queries';
import { workspaceId } from '../../api/session';
import type { AnalyticsMetrics } from '../../api/types';
const scope = ref('all');
const cadence = ref('daily');
const range = ref('30d');
type Metric = 'tokens' | 'cost' | 'time' | 'studies';
const metric = ref<Metric>('tokens');
const metrics: { key: Metric; label: string; summary: string }[] = [
  { key: 'cost', label: 'Cost', summary: 'Cost' },
  { key: 'tokens', label: 'Tokens', summary: 'Tokens' },
  { key: 'time', label: 'Time', summary: 'Run time' },
  { key: 'studies', label: 'Studies', summary: 'Studies' },
];
const researches = useResearches();
const analytics = useAnalytics(scope, cadence, range);
watch(workspaceId, () => {
  scope.value = 'all';
});
function value(item: AnalyticsMetrics, key: Metric) {
  return key === 'tokens'
    ? item.input_tokens + item.output_tokens
    : key === 'cost'
      ? item.cost_usd
      : key === 'time'
        ? item.runtime_seconds
        : item.studies;
}
const number = (n: number) =>
  Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(n);
const dollars = (n: number) =>
  `$${n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: n > 0 && n < 0.01 ? 6 : 2 })}`;
function format(item: AnalyticsMetrics, key: Metric) {
  if (item.coverage[key] === 'missing') return '—';
  const n = value(item, key);
  if (key === 'cost') return dollars(n);
  if (key === 'time')
    return n < 60 ? `${number(n)}s` : n < 3600 ? `${number(n / 60)}m` : `${number(n / 3600)}h`;
  return number(n);
}
function tooltip(item: AnalyticsMetrics) {
  return `Ordinary input: ${item.ordinary_input_tokens.toLocaleString()}; cache read: ${item.cache_read_tokens.toLocaleString()}; cache write: ${item.cache_write_tokens.toLocaleString()}; output: ${item.output_tokens.toLocaleString()}. Input includes caches.`;
}
function coverage(item: AnalyticsMetrics, key: Metric) {
  if (item.coverage[key] === 'partial') return 'Partial';
  if (item.coverage[key] === 'missing')
    return key === 'tokens' || key === 'cost' ? 'Usage not reported' : 'Measurement unavailable';
  return '';
}
const agents = computed(() =>
  [...(analytics.data.value?.agents ?? [])].sort((a, b) => {
    const missing =
      Number(a.coverage[metric.value] === 'missing') -
      Number(b.coverage[metric.value] === 'missing');
    return (
      missing || value(b, metric.value) - value(a, metric.value) || a.name.localeCompare(b.name)
    );
  }),
);
const maximum = computed(() =>
  Math.max(
    0,
    ...(analytics.data.value?.buckets ?? [])
      .filter((b) => b.coverage[metric.value] !== 'missing')
      .map((b) => value(b, metric.value)),
  ),
);
const height = (n: number) => `${maximum.value ? (n / maximum.value) * 100 : 0}%`;
const bucketDate = (date: string) =>
  new Date(date).toLocaleDateString(undefined, {
    timeZone: analytics.data.value?.timezone ?? 'UTC',
    month: 'short',
    day: 'numeric',
  });
const updated = computed(() =>
  analytics.data.value
    ? new Date(analytics.data.value.now).toLocaleTimeString([], {
        hour: '2-digit',
        minute: '2-digit',
      })
    : '',
);
</script>
<template>
  <section class="analytics" data-testid="analytics-panel" aria-label="Analytics">
    <header class="section-heading">
      <h3>Analytics</h3>
      <span v-if="analytics.data.value">Updated {{ updated }}</span>
    </header>
    <div class="toolbar">
      <select v-model="scope" aria-label="Research scope">
        <option value="all">All Studies</option>
        <option value="standalone">Standalone Studies</option>
        <option v-for="research in researches.data.value" :key="research.id" :value="research.id">
          {{ research.name }}
        </option>
      </select>
      <div class="segments" role="group" aria-label="Cadence">
        <button
          v-for="item in ['daily', 'weekly']"
          :key="item"
          :aria-pressed="cadence === item"
          @click="cadence = item"
        >
          {{ item === 'daily' ? 'Daily' : 'Weekly' }}
        </button>
      </div>
      <div class="segments" role="group" aria-label="Date range">
        <button
          v-for="item in ['1d', '7d', '30d', '90d']"
          :key="item"
          :aria-pressed="range === item"
          @click="range = item"
        >
          {{ item }}
        </button>
      </div>
    </div>
    <p v-if="researches.isError.value" class="muted">Research choices could not be loaded.</p>
    <p v-if="analytics.isPending.value" role="status" class="loading">Loading analytics…</p>
    <div v-else-if="analytics.isError.value" role="alert" class="error">
      Could not load analytics. {{ analytics.error.value?.message
      }}<button class="button compact" @click="analytics.refetch()">Retry</button>
    </div>
    <template v-else-if="analytics.data.value">
      <div class="summaries" data-testid="analytics-summary">
        <div
          v-for="item in metrics"
          :key="item.key"
          :title="item.key === 'tokens' ? tooltip(analytics.data.value.summary) : undefined"
        >
          <span>{{ item.summary }}</span
          ><strong>{{ format(analytics.data.value.summary, item.key) }}</strong>
          <small v-if="coverage(analytics.data.value.summary, item.key)">{{
            coverage(analytics.data.value.summary, item.key)
          }}</small>
          <small v-if="item.key === 'cost' && analytics.data.value.summary.includes_estimates"
            >Includes
            {{ dollars(analytics.data.value.summary.cost_estimated_usd) }} estimated</small
          >
        </div>
      </div>
      <p
        v-if="
          analytics.data.value.summary.studies === 0 &&
          analytics.data.value.summary.coverage.studies === 'complete'
        "
        class="empty"
      >
        No executed study activity in this period.
      </p>
      <div class="chart-panel">
        <header>
          <h4>Usage</h4>
          <div class="segments" role="group" aria-label="Chart metric">
            <button
              v-for="item in metrics"
              :key="item.key"
              :aria-pressed="metric === item.key"
              @click="metric = item.key"
            >
              {{ item.label }}
            </button>
          </div>
        </header>
        <div class="chart-scale muted">
          {{
            format(
              {
                ...analytics.data.value.summary,
                ...(metric === 'tokens'
                  ? { input_tokens: maximum, output_tokens: 0 }
                  : metric === 'cost'
                    ? { cost_usd: maximum }
                    : metric === 'time'
                      ? { runtime_seconds: maximum }
                      : { studies: maximum }),
              },
              metric,
            )
          }}
        </div>
        <div class="chart-scroll">
          <div
            class="chart"
            data-testid="analytics-chart"
            :aria-label="`${metric} by ${cadence} bucket`"
          >
            <div
              v-for="bucket in analytics.data.value.buckets"
              :key="bucket.start"
              class="bucket"
              :title="`${bucketDate(bucket.start)}: ${format(bucket, metric)} ${coverage(bucket, metric)}${metric === 'tokens' ? '. ' + tooltip(bucket) : ''}`"
            >
              <div class="bar-area" :class="{ incomplete: bucket.coverage[metric] !== 'complete' }">
                <span
                  v-if="bucket.coverage[metric] === 'missing'"
                  class="missing"
                  aria-label="Measurement unavailable"
                  >?</span
                >
                <template v-else-if="metric === 'tokens'"
                  ><span class="bar output" :style="{ height: height(bucket.output_tokens) }"></span
                  ><span class="bar input" :style="{ height: height(bucket.input_tokens) }"></span
                ></template>
                <span
                  v-else
                  class="bar input"
                  :style="{ height: height(value(bucket, metric)) }"
                ></span>
                <span
                  v-if="bucket.coverage[metric] === 'partial'"
                  class="partial-marker"
                  aria-label="Partial measurement"
                  >*</span
                >
              </div>
              <small>{{ bucketDate(bucket.start) }}</small>
            </div>
          </div>
        </div>
        <footer v-if="metric === 'tokens'">
          <span><i class="input"></i>Input (including caches)</span
          ><span><i class="output"></i>Output</span>
        </footer>
        <p
          v-if="analytics.data.value.buckets.some((b) => b.coverage[metric] !== 'complete')"
          class="basis"
        >
          ? unavailable · * recorded partial value. Missing measurements are not zero.
        </p>
      </div>
      <div class="leaderboard">
        <header>
          <h4>Agents</h4>
          <span class="muted"
            >Sorted by {{ metrics.find((m) => m.key === metric)?.label.toLowerCase() }}</span
          >
        </header>
        <div class="table-scroll">
          <table aria-label="Agent usage">
            <thead>
              <tr>
                <th>Agent</th>
                <th
                  v-for="item in metrics"
                  :key="item.key"
                  :aria-sort="metric === item.key ? 'descending' : 'none'"
                >
                  <button @click="metric = item.key">
                    {{ item.label }}{{ metric === item.key ? ' ↓' : '' }}
                  </button>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="agent in agents" :key="agent.agent_id ?? 'unattributed'">
                <td>
                  {{ agent.agent_id ? agent.name : 'Unattributed'
                  }}<small v-if="!agent.agent_id">{{
                    [...agent.roles, ...agent.harnesses].join(' · ')
                  }}</small>
                </td>
                <td
                  v-for="item in metrics"
                  :key="item.key"
                  :title="item.key === 'tokens' ? tooltip(agent) : undefined"
                >
                  {{ format(agent, item.key)
                  }}<small v-if="coverage(agent, item.key)">{{ coverage(agent, item.key) }}</small
                  ><small v-if="item.key === 'cost' && agent.includes_estimates"
                    >Includes estimates</small
                  >
                </td>
              </tr>
              <tr v-if="!agents.length">
                <td colspan="5" class="muted">No agent activity in this period.</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
      <p
        v-if="
          analytics.data.value.summary.coverage.missing_usage_attempts ||
          analytics.data.value.summary.coverage.missing_timing_attempts
        "
        class="coverage-note"
      >
        Coverage: {{ analytics.data.value.summary.coverage.missing_usage_attempts }} attempts
        without complete usage;
        {{ analytics.data.value.summary.coverage.missing_timing_attempts }} attempts without
        complete timing.
      </p>
      <p v-if="analytics.data.value.unassigned.attempts" class="coverage-note">
        Outside the dated total: {{ analytics.data.value.unassigned.attempts }} attempts have no
        recoverable end time. Recorded
        {{
          number(
            analytics.data.value.unassigned.input_tokens +
              analytics.data.value.unassigned.output_tokens,
          )
        }}
        tokens and {{ dollars(analytics.data.value.unassigned.cost_usd)
        }}{{ analytics.data.value.unassigned.includes_estimates ? ' (includes estimates)' : '' }}
        remain unassigned to a date in this scope.
      </p>
    </template>
  </section>
</template>
<style scoped>
.analytics {
  margin-top: 32px;
  font-size: 13px;
}
h3,
h4 {
  font-size: 14px;
  line-height: 20px;
  font-weight: 600;
  margin: 0;
}
.section-heading,
.chart-panel header,
.leaderboard header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 16px;
}
.section-heading > span,
.basis,
.muted,
.coverage-note {
  color: var(--muted);
  font-size: 11px;
}
.toolbar {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
  margin-bottom: 14px;
}
select {
  background: var(--input);
  border: 1px solid var(--border);
  color: var(--text);
  border-radius: 7px;
  padding: 7px 10px;
  min-width: 150px;
  max-width: 100%;
  margin-right: auto;
}
.segments {
  display: flex;
  background: var(--input);
  padding: 3px;
  border-radius: 7px;
  border: 1px solid var(--border);
}
.segments button {
  padding: 4px 9px;
  color: var(--muted);
  font-size: 12px;
  border-radius: 4px;
  white-space: nowrap;
}
.segments button[aria-pressed='true'] {
  background: var(--surface);
  color: var(--text);
  box-shadow: 0 1px 2px #0001;
}
.basis {
  line-height: 18px;
  margin: 0 0 14px;
}
.summaries {
  background: var(--input);
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  border: 1px solid var(--border);
  border-radius: 10px;
  overflow: hidden;
  margin-bottom: 20px;
}
.summaries > div {
  padding: 18px 20px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
}
.summaries > div + div {
  border-left: 1px solid var(--border);
}
.summaries span {
  color: var(--muted);
  font-size: 12px;
}
.summaries strong {
  font-size: 24px;
  line-height: 30px;
  font-weight: 500;
}
small {
  color: var(--muted);
  font-size: 10px;
  line-height: 15px;
}
.chart-panel,
.leaderboard {
  background: var(--input);
  padding: 20px;
  border: 1px solid var(--border);
  border-radius: 10px;
  margin-bottom: 20px;
}
.chart-scale {
  margin-bottom: 6px;
}
.chart-scroll,
.table-scroll {
  overflow-x: auto;
}
.chart {
  display: flex;
  height: 175px;
  gap: 4px;
  min-width: 350px;
}
.bucket {
  flex: 1;
  min-width: 3px;
  display: flex;
  flex-direction: column;
  align-items: center;
}
.bar-area {
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
  align-items: stretch;
  width: 100%;
  height: 145px;
  position: relative;
  border-bottom: 1px solid var(--border);
}
.bar {
  display: block;
  flex-shrink: 0;
}
.input {
  background: var(--primary);
}
.output {
  background: var(--selected);
}
.bucket small {
  white-space: nowrap;
  font-size: 9px;
  margin-top: 8px;
}
.bucket:not(:first-child):not(:last-child) small {
  visibility: hidden;
}
.bucket:nth-child(7n) small {
  visibility: visible;
}
.missing {
  text-align: center;
  color: var(--muted);
}
.incomplete {
  background: repeating-linear-gradient(135deg, transparent 0 4px, var(--border) 4px 5px);
}
.partial-marker {
  position: absolute;
  top: 0;
  right: 0;
  color: var(--muted);
}
.chart-panel footer {
  display: flex;
  gap: 16px;
  margin-top: 14px;
  color: var(--muted);
  font-size: 11px;
}
.chart-panel footer i {
  display: inline-block;
  height: 8px;
  width: 8px;
  border-radius: 2px;
  margin-right: 5px;
}
.chart-panel > .basis {
  margin: 12px 0 0;
}
.leaderboard header .muted {
  font-size: 11px;
}
table {
  width: 100%;
  border-collapse: collapse;
  text-align: right;
  font-size: 12px;
}
th,
td {
  padding: 12px 8px;
  border-bottom: 1px solid var(--border);
}
th {
  font-weight: 400;
  color: var(--muted);
  font-size: 11px;
}
th:first-child,
td:first-child {
  text-align: left;
  padding-left: 0;
}
th button {
  white-space: nowrap;
}
td small {
  display: block;
}
tr:last-child td {
  border: 0;
}
.coverage-note {
  line-height: 18px;
  margin: 12px 0;
}
.loading,
.empty {
  color: var(--muted);
  padding: 20px 0;
}
.error {
  color: var(--error-text);
  padding: 20px 0;
}
.error button {
  margin-left: 12px;
}
@media (width < 48rem) {
  .summaries {
    grid-template-columns: repeat(2, 1fr);
  }
  .summaries > div {
    padding: 14px;
  }
  .summaries > div:nth-child(3) {
    border-left: 0;
  }
  .summaries > div:nth-child(n + 3) {
    border-top: 1px solid var(--border);
  }
  .chart-panel,
  .leaderboard {
    padding: 14px;
  }
  .chart-panel header {
    flex-wrap: wrap;
  }
  .toolbar {
    gap: 8px;
  }
  .chart-panel footer {
    flex-wrap: wrap;
    gap: 8px;
  }
}
</style>
