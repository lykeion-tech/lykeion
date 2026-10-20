<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue';
import type { AgentRun } from '../api/types';
import {
  activeRun,
  durationLabel,
  milliseconds,
  runDuration,
  runTimeline,
} from '../lib/run-timeline';
import { roleLabel } from '../lib/studies';
import Icon from './Icon.vue';
import Modal from './Modal.vue';
const props = defineProps<{ runs: AgentRun[]; title: string }>();
const expanded = ref(true),
  showRuns = ref(false),
  clock = ref(Date.now());
const interval = setInterval(() => {
  clock.value = Date.now();
}, 1000);
onUnmounted(() => clearInterval(interval));
const data = computed(() => runTimeline(props.runs, clock.value));
const rows = computed(() => [
  ...data.value.runs.filter(activeRun),
  ...data.value.runs
    .filter((r) => !activeRun(r))
    .reverse()
    .slice(0, 3),
]);
const lanes = computed(() => [...new Set(data.value.runs.map((r) => r.role))]);
const money = (n: number) => `$${n.toFixed(2)}`;
const stamp = (n: number) =>
  new Date(n).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
const count = (n: number) => `${n} ${n === 1 ? 'run' : 'runs'}`;
function position(run: AgentRun) {
  const start = milliseconds(run.started_at);
  if (start === null) return {};
  const end = milliseconds(run.completed_at) ?? (run.status === 'running' ? clock.value : start);
  return {
    left: data.value.x(start) / 10 + '%',
    width: Math.max(0.4, (data.value.x(end) - data.value.x(start)) / 10) + '%',
  };
}
</script>
<template>
  <section class="execution-log">
    <div class="execution-heading">
      <h3>
        <button :aria-expanded="expanded" @click="expanded = !expanded">
          Execution log <Icon :name="expanded ? 'down' : 'right'" :size="11" />
        </button>
      </h3>
      <button class="execution-total" aria-label="Open Runs" @click="showRuns = true">
        {{ count(runs.length) }} · {{ money(data.total) }}
      </button>
    </div>
    <div v-if="expanded" class="execution-body">
      <button class="spend-strip" aria-label="Open cost timeline" @click="showRuns = true">
        <svg
          class="cost-plot"
          viewBox="0 0 1000 100"
          preserveAspectRatio="none"
          aria-label="Recorded cumulative cost"
        >
          <path :d="data.area" class="cost-fill" />
          <path :d="data.path" class="cost-stroke" /></svg
        ><span class="duration-track"
          ><i
            v-for="run in data.runs.filter((r) => r.started_at)"
            :key="run.id"
            :style="position(run)"
        /></span>
      </button>
      <div v-if="runs.length" class="time-axis">
        <time>{{ stamp(data.first) }}</time
        ><time>{{ stamp(data.last) }}</time>
      </div>
      <p v-if="runs.length" class="execution-timing">
        {{ durationLabel(data.agentMs) }}
        {{ data.partial ? 'latest-attempt time' : 'agent time' }} ·
        {{ durationLabel(data.elapsed) }} elapsed
      </p>
      <p v-else class="execution-timing">No runs yet.</p>
      <p v-if="rows.some((r) => !activeRun(r))" class="latest-label">Latest</p>
      <div
        v-for="run in rows"
        :key="run.id"
        class="execution-row"
        :data-status="run.status"
        :title="`${run.status} · ${run.board_column} · Round ${run.round} · Attempt ${run.attempt}`"
      >
        <span class="avatar">{{ roleLabel(run.role)[0] }}</span
        ><span class="run-label"
          >{{ roleLabel(run.role)
          }}<small>Round {{ run.round }} · Attempt {{ run.attempt || '—' }}</small></span
        ><span class="run-outcome"
          ><span v-if="activeRun(run)">{{ run.status }}</span
          ><span v-else class="run-status-mark" :class="run.status">{{
            run.status === 'completed' ? '✓' : run.status === 'failed' ? '!' : '⊘'
          }}</span>
          {{ money(run.cost_usd) }}</span
        >
      </div>
      <button class="open-timeline" @click="showRuns = true">
        <Icon name="expand" :size="12" />Open timeline
      </button>
    </div>
  </section>
  <Modal v-if="showRuns" title="Runs" wide class="runs-modal" @close="showRuns = false">
    <div class="runs-dialog">
      <header class="runs-title">
        <h2>Runs</h2>
        <button class="icon-button" aria-label="Close Runs" @click="showRuns = false">
          <Icon name="close" :size="16" />
        </button>
      </header>
      <p class="runs-subtitle">{{ title }}</p>
      <div class="run-metrics">
        <div>
          <small>Spent</small><strong>{{ money(data.total) }}</strong>
        </div>
        <div>
          <small>{{ data.partial ? 'Latest-attempt time' : 'Agent time' }}</small
          ><span>{{ durationLabel(data.agentMs) }}</span>
        </div>
        <div>
          <small>Elapsed</small><span>{{ durationLabel(data.elapsed) }}</span>
        </div>
        <div>
          <small>Runs</small><span>{{ runs.length }}</span>
        </div>
      </div>
      <div class="cost-legend">
        <span>— Cumulative recorded cost</span><span>▪ Run · width is duration</span>
      </div>
      <div class="timeline-chart">
        <svg
          class="cost-plot"
          viewBox="0 0 1000 100"
          preserveAspectRatio="none"
          aria-label="Recorded cumulative cost"
        >
          <path :d="data.area" class="cost-fill" />
          <path :d="data.path" class="cost-stroke" /></svg
        ><span class="cost-axis">{{ money(data.total) }}</span>
      </div>
      <div v-for="role in lanes" :key="role" class="timeline-lane">
        <span>{{ roleLabel(role) }}</span>
        <div class="duration-track">
          <i
            v-for="run in data.runs.filter((r) => r.role === role && r.started_at)"
            :key="run.id"
            :style="position(run)"
            :title="`${roleLabel(role)} · ${durationLabel(runDuration(run, clock))}`"
          />
        </div>
      </div>
      <div class="time-axis chart-time-axis">
        <time>{{ new Date(data.first).toLocaleString() }}</time
        ><time>{{ new Date(data.last).toLocaleString() }}</time>
      </div>
      <p v-if="data.partial" class="timing-note">
        Full attempt timing is unavailable. Agent time shows recorded latest attempts only.
      </p>
      <section v-for="[day, group] in data.groups" :key="day" class="run-day">
        <header>
          <span>{{ day }}</span
          ><span
            >{{ count(group.length) }} ·
            {{ money(group.reduce((sum, r) => sum + r.cost_usd, 0)) }}</span
          >
        </header>
        <div v-for="run in group" :key="run.id" class="timeline-row">
          <time>{{ stamp(milliseconds(run.started_at ?? run.created_at) ?? clock) }}</time
          ><span class="timeline-role"
            >{{ roleLabel(run.role)
            }}<small
              >{{ run.board_column }} · Round {{ run.round }} · Attempt
              {{ run.attempt || '—' }}</small
            ></span
          ><span>{{ run.status }}</span
          ><span>{{ durationLabel(runDuration(run, clock)) }}</span
          ><span>{{ money(run.cost_usd) }}</span>
        </div>
      </section>
      <p class="timing-note">Costs are recorded by the server. Token breakdown is unavailable.</p>
    </div>
  </Modal>
</template>
<style scoped>
.execution-heading {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.execution-heading h3 {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 20px;
}
.execution-heading h3 button {
  padding: 0;
  font: inherit;
}
.execution-heading button {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  white-space: nowrap;
}
.execution-heading .execution-total {
  margin-left: auto;
  color: var(--muted);
  font-size: 12px;
}
.spend-strip {
  width: 100%;
  display: block;
  padding: 4px 4px 0;
}
.cost-plot {
  display: block;
  width: 100%;
  height: 44px;
  overflow: visible;
}
.cost-fill {
  fill: #327dcb18;
}
.cost-stroke {
  fill: none;
  stroke: #327dcb;
  stroke-width: 1.5;
  vector-effect: non-scaling-stroke;
}
.duration-track {
  display: block;
  height: 6px;
  background: var(--border);
  border-radius: 3px;
  position: relative;
  margin-top: 4px;
}
.duration-track i {
  display: block;
  position: absolute;
  top: 0;
  height: 100%;
  min-width: 2px;
  background: #4287cd;
  border-radius: 1px;
}
.time-axis {
  display: flex;
  justify-content: space-between;
  font-size: 11px;
  color: var(--muted);
  margin: 3px 4px;
}
.execution-timing,
.latest-label {
  font-size: 12px;
  color: var(--muted);
  margin: 8px 4px 14px;
}
.execution-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 4px;
  font-size: 12px;
}
.run-label {
  flex: 1;
  min-width: 0;
  overflow-wrap: anywhere;
}
.run-label small {
  display: block;
  font-size: 10px;
  color: var(--muted);
  margin-top: 2px;
}
.run-outcome {
  margin-left: auto;
  font-size: 11px;
  white-space: nowrap;
}
.run-status-mark {
  color: var(--muted);
}
.run-status-mark.completed {
  color: #39824e;
}
.run-status-mark.failed {
  color: var(--error-text);
}
.open-timeline {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 10px 4px;
  font-size: 12px;
}
.runs-modal {
  width: 1028px !important;
}
.runs-dialog {
  width: 100%;
  padding: 20px 24px;
  min-height: min(850px, 85vh);
}
.runs-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.runs-title h2 {
  font-size: 16px;
  font-weight: 500;
}
.runs-subtitle {
  font-size: 13px;
  color: var(--muted);
  margin-top: 6px;
}
.run-metrics {
  display: flex;
  align-items: baseline;
  gap: 32px;
  margin: 20px 0;
}
.run-metrics > div {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.run-metrics small {
  text-transform: uppercase;
  font-size: 10px;
  color: var(--muted);
}
.run-metrics strong {
  font-size: 24px;
  font-weight: 600;
}
.run-metrics span {
  font-size: 15px;
}
.cost-legend {
  display: flex;
  justify-content: flex-end;
  gap: 16px;
  font-size: 11px;
  color: var(--muted);
  margin: 10px 0 24px;
}
.timeline-chart {
  position: relative;
  margin: 0 50px 12px 110px;
  background: repeating-linear-gradient(
    to top,
    transparent 0,
    transparent calc(25% - 1px),
    var(--border) 25%
  );
}
.timeline-chart .cost-plot {
  height: 150px;
}
.cost-axis {
  position: absolute;
  left: calc(100% + 10px);
  top: 0;
  font-size: 11px;
  color: var(--muted);
  white-space: nowrap;
}
.timeline-lane {
  display: flex;
  gap: 10px;
  align-items: center;
  margin: 8px 50px 8px 0;
  font-size: 11px;
  color: var(--muted);
}
.timeline-lane > span {
  width: 100px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.timeline-lane .duration-track {
  flex: 1;
  margin: 0;
  height: 9px;
}
.chart-time-axis {
  margin-left: 110px;
  margin-right: 50px;
}
.timing-note {
  font-size: 11px;
  color: var(--muted);
  margin-top: 18px;
  line-height: 18px;
}
.run-day {
  margin-top: 28px;
}
.run-day > header {
  display: flex;
  justify-content: space-between;
  font-size: 11px;
  color: var(--muted);
  padding-bottom: 8px;
}
.timeline-row {
  display: flex;
  align-items: center;
  gap: 16px;
  border-top: 1px solid var(--border);
  padding: 10px 0;
  font-size: 12px;
}
.timeline-row time {
  width: 60px;
  flex: none;
  white-space: nowrap;
  font-size: 10px;
  color: var(--muted);
}
.timeline-role {
  flex: 1;
  min-width: 0;
}
.timeline-role small {
  display: block;
  font-size: 10px;
  color: var(--muted);
  margin-top: 3px;
}
.timeline-row > span:not(.timeline-role) {
  width: 70px;
}
.timeline-row > span:last-child {
  text-align: right;
}
@media (max-width: 650px) {
  .run-metrics {
    gap: 15px;
    flex-wrap: wrap;
  }
  .runs-dialog {
    padding: 16px;
  }
  .timeline-chart {
    margin-left: 0;
  }
  .timeline-lane > span {
    width: 70px;
  }
  .chart-time-axis {
    margin-left: 0;
  }
  .timeline-row {
    gap: 8px;
    font-size: 11px;
  }
  .timeline-row > span:not(.timeline-role) {
    width: 48px;
  }
}
</style>
