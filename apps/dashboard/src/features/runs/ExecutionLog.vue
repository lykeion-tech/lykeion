<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue';
import type { AgentRun } from '../../api/types';
import { durationLabel, milliseconds, runDuration, runTimeline } from './run-timeline';
import { roleLabel } from '../../lib/studies';
const props = defineProps<{ runs: AgentRun[] }>();
const clock = ref(Date.now());
const interval = setInterval(() => {
  clock.value = Date.now();
}, 1000);
onUnmounted(() => clearInterval(interval));
const data = computed(() => runTimeline(props.runs, clock.value));
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
  <section class="execution-log" aria-label="Execution log">
    <div class="runs-dialog">
      <header class="runs-title">
        <h2>Execution log</h2>
      </header>
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
      <p v-if="!runs.length" class="timing-note">No runs yet.</p>
      <template v-if="runs.length">
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
      </template>
      <p class="timing-note">Costs are recorded by the server. Token breakdown is unavailable.</p>
    </div>
  </section>
</template>
<style scoped>
.execution-log {
  container-type: inline-size;
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
.runs-dialog {
  width: 100%;
  min-width: 0;
}
.runs-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.runs-title h2 {
  font-size: 16px;
  font-weight: 700;
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
  font-size: 15px;
  font-weight: 600;
}
.run-metrics span {
  font-size: 15px;
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
  font-size: 13px;
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
@container (max-width: 650px) {
  .run-metrics {
    gap: 15px;
    flex-wrap: wrap;
  }
  .timeline-chart {
    margin-left: 0;
  }
  .timeline-lane > span {
    width: 45px;
  }
  .chart-time-axis {
    margin-left: 0;
    margin-right: 0;
    gap: 8px;
  }
  .chart-time-axis time:last-child {
    text-align: right;
  }
  .timeline-row {
    gap: 6px;
    font-size: 13px;
    flex-wrap: wrap;
  }
  .timeline-row > span:not(.timeline-role) {
    width: auto;
  }
  .timeline-role {
    flex-basis: calc(100% - 70px);
  }
  .timeline-row > span:last-child {
    margin-left: auto;
  }
}
</style>
