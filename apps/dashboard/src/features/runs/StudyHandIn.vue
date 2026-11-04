<script setup lang="ts">
import { computed } from 'vue';
import RecordedMarkdown from '../../ui/RecordedMarkdown.vue';

const props = defineProps<{
  handIn: Record<string, unknown>;
  role: string;
  column: string;
  reviewState?: 'unknown' | 'pending' | 'available';
}>();
const labels: Record<string, string> = {
  papers: 'Sources',
  datasets: 'Datasets',
  no_qualifying: 'No qualifying sources',
  hypotheses: 'Hypotheses',
  outputs: 'Outputs',
  values: 'Recorded values',
  assessments: 'Assessments',
  verdicts: 'Claim verdicts',
  flagged_errors: 'Flagged errors',
  objections: 'Objections',
  concerns: 'Concerns',
  replies: 'Responses to review',
  decision: 'Question for the researcher',
  reason: 'Reason',
  report: 'Report',
  new_studies: 'New studies',
};
const title = computed(() =>
  props.role === 'auditor'
    ? 'Audit report'
    : props.role !== 'leader' && props.role !== 'agent'
      ? 'Member report'
      : `${props.column.charAt(0).toUpperCase()}${props.column.slice(1)} report`,
);
const stageFields: Record<string, string[]> = {
  grounding: ['papers', 'datasets', 'no_qualifying', 'replies', 'decision', 'reason'],
  hypotheses: ['hypotheses', 'replies', 'decision', 'reason'],
  experimentation: ['values', 'outputs', 'replies', 'decision', 'reason'],
  validation: [
    'assessments',
    'verdicts',
    'flagged_errors',
    'objections',
    'replies',
    'decision',
    'reason',
  ],
};
const fields = computed(() =>
  props.role === 'auditor'
    ? ['concerns', 'replies', 'decision', 'reason']
    : [...(stageFields[props.column] ?? []), ...Object.keys(labels)],
);
const sections = computed(() =>
  [...new Set(fields.value)].flatMap((key) => {
    const label = labels[key];
    if (!label) return [];
    const value = props.handIn[key];
    if (value == null || value === '' || (Array.isArray(value) && !value.length)) return [];
    return [{ key, label, value: Array.isArray(value) ? value : [value] }];
  }),
);
const status = computed(() =>
  props.handIn.status === 'could_not_do'
    ? 'Could not complete'
    : props.handIn.status === 'ask'
      ? 'Question for the researcher'
      : props.handIn.status === 'done' || props.handIn.status == null
        ? ''
        : `Recorded status: ${String(props.handIn.status)}`,
);
function sourceURL(text: string): string {
  const match = /^doi:(10\.\d{4,9}\/[^\s]+)$/i.exec(text);
  return match ? `https://doi.org/${match[1]}` : '';
}
function entries(value: unknown, prefix = ''): [string, string][] {
  if (value == null || value === '') return [];
  if (Array.isArray(value)) return value.flatMap((item) => entries(item, prefix));
  if (typeof value === 'object')
    return Object.entries(value as Record<string, unknown>).flatMap(([key, item]) =>
      entries(item, [prefix, key.replace(/_/g, ' ')].filter(Boolean).join(' · ')),
    );
  return [[prefix, String(value)]];
}
</script>
<template>
  <section class="study-hand-in" :aria-label="title">
    <h3>{{ title }}</h3>
    <p v-if="status" class="report-status">{{ status }}</p>
    <p v-if="handIn.status === 'could_not_do' && handIn.reason">{{ handIn.reason }}</p>
    <h4 v-if="typeof handIn.answer === 'string'">
      {{
        role === 'auditor'
          ? 'Conclusion'
          : role !== 'leader' && role !== 'agent'
            ? 'Findings and handoff'
            : 'Summary'
      }}
    </h4>
    <RecordedMarkdown
      v-if="typeof handIn.answer === 'string'"
      class="lyk-reading"
      :content="handIn.answer"
    />
    <details v-else>
      <summary>Recorded hand-in</summary>
      <pre>{{ JSON.stringify(handIn, null, 2) }}</pre>
    </details>
    <p v-if="column === 'validation'">
      {{
        reviewState === 'pending'
          ? 'Review pending.'
          : reviewState === 'available'
            ? 'See Review for check results.'
            : 'Review status unavailable.'
      }}
    </p>
    <details
      v-for="section in sections"
      :key="section.key"
      :open="
        role === 'auditor'
          ? section.key === 'concerns'
          : (role === 'leader' || role === 'agent') && section.key !== 'replies'
      "
    >
      <summary>{{ section.label }}</summary>
      <div v-for="(item, index) in section.value" :key="index" class="hand-in-item">
        <p v-for="([name, text], i) in entries(item)" :key="i">
          <strong v-if="name">{{ name }}: </strong
          ><a v-if="sourceURL(text)" :href="sourceURL(text)">{{ text }}</a
          ><template v-else>{{ text }}</template>
        </p>
      </div>
    </details>
    <details>
      <summary>Recorded hand-in data</summary>
      <pre>{{ JSON.stringify(handIn, null, 2) }}</pre>
    </details>
  </section>
</template>
<style scoped>
.study-hand-in {
  min-width: 0;
  overflow-wrap: anywhere;
}
.study-hand-in h3 {
  font-size: 14px;
  font-weight: 600;
  margin: 0 0 8px;
}
.study-hand-in details {
  margin-top: 8px;
}
.study-hand-in summary {
  cursor: pointer;
  font-weight: 600;
}
.study-hand-in pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.hand-in-item {
  padding: 8px 0 0 12px;
}
.hand-in-item p {
  margin: 3px 0;
}
</style>
