<script setup lang="ts">
import { computed } from 'vue';
import { useKindRecords, useRuntimes } from '../../api/queries';
import type { AgentRun } from '../../api/types';
import { environmentView } from './environment';

const props = defineProps<{ id: string; runs: AgentRun[] }>();
const runtimes = useRuntimes();
const records = useKindRecords(() => props.id, 'run');
const view = computed(() =>
  environmentView(props.runs, runtimes.data.value ?? [], records.data.value ?? []),
);
const chip = { Ready: 'chip-ready', Building: 'chip-working', Failed: 'chip-blocked' } as Record<
  string,
  string
>;
</script>
<template>
  <div class="environment-tab">
    <section aria-labelledby="env-machines">
      <h2 id="env-machines">Machines</h2>
      <p v-if="runtimes.isError.value" class="inline-error" role="alert">
        {{ runtimes.error.value?.message }}
        <button class="text-button" @click="runtimes.refetch()">Retry machines</button>
      </p>
      <p v-else-if="runtimes.isSuccess.value && !view.machines.length" class="muted">
        No machine has run this study yet.
      </p>
      <ul v-else class="env-machines">
        <li v-for="(m, i) in view.machines" :key="i">
          <span class="env-machine-name">{{ m.name }}</span>
          <span v-for="b in m.bases" :key="b.language" class="base">
            <span class="base-language">{{ b.language }}</span
            ><span class="chip" :class="chip[b.state]">{{ b.state }}</span
            ><span v-if="b.reason" class="base-reason">{{ b.reason }}</span>
          </span>
        </li>
      </ul>
    </section>
    <section aria-labelledby="env-packages" class="env-packages">
      <h2 id="env-packages">Packages added by this study</h2>
      <p v-if="records.isError.value" class="inline-error" role="alert">
        {{ records.error.value?.message }}
        <button class="text-button" @click="records.refetch()">Retry packages</button>
      </p>
      <p v-else-if="records.isSuccess.value && !view.packages.length" class="muted">
        This study hasn't added any packages.
      </p>
      <div v-for="p in view.packages" :key="p.language" class="env-package">
        <span class="base-language">{{ p.language }}</span>
        <pre tabindex="0">{{ p.text }}</pre>
      </div>
    </section>
  </div>
</template>
<style scoped>
.environment-tab {
  display: flex;
  flex-direction: column;
  gap: 24px;
}
h2 {
  font-size: 13.5px;
  margin-bottom: 8px;
}
.env-machines {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.env-machines li {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 16px;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  font-size: 13px;
  min-width: 0;
}
.env-machine-name {
  flex-basis: 100%;
  overflow-wrap: anywhere;
}
.base {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  min-width: 0;
}
.base-language {
  color: var(--muted);
  font-size: 12px;
}
.base-reason {
  color: var(--muted);
  font-size: 12px;
  overflow-wrap: anywhere;
}
.env-package + .env-package {
  margin-top: 14px;
}
.env-package pre {
  margin: 4px 0 0;
  padding: 10px 12px;
  max-height: 320px;
  overflow: auto;
  border-radius: 6px;
  background: var(--fill-inset);
  color: var(--text);
  user-select: text;
  font:
    12px/1.6 ui-monospace,
    SFMono-Regular,
    Menlo,
    monospace;
}
</style>
