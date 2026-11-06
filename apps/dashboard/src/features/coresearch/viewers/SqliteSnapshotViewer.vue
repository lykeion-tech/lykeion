<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { SqliteSnapshot } from './sqlite-snapshot';
const props = defineProps<{ blob: Blob }>();
const state = ref<'loading' | 'ready' | 'unavailable'>('loading'),
  snapshot = ref<SqliteSnapshot>(),
  selected = ref(0);
const table = computed(() => snapshot.value?.tables[selected.value]);
const width = computed(() => Math.max(0, ...(table.value?.rows ?? []).map((r) => r.fields.length)));
watch(
  () => props.blob,
  (blob, _, cleanup) => {
    state.value = 'loading';
    snapshot.value = undefined;
    selected.value = 0;
    if (blob.size > 4 * 1024 * 1024 || typeof Worker === 'undefined') {
      state.value = 'unavailable';
      return;
    }
    let worker: Worker;
    try {
      worker = new Worker(new URL('./sqlite-snapshot.worker.ts', import.meta.url), {
        type: 'module',
      });
    } catch {
      state.value = 'unavailable';
      return;
    }
    let active = true;
    const done = () => {
      active = false;
      clearTimeout(timeout);
      worker.terminate();
    };
    const timeout = setTimeout(() => {
      if (active) {
        done();
        state.value = 'unavailable';
      }
    }, 10000);
    cleanup(() => done());
    worker.onmessage = (event: MessageEvent<SqliteSnapshot | null>) => {
      if (!active) return;
      done();
      snapshot.value = event.data ?? undefined;
      state.value = event.data ? 'ready' : 'unavailable';
    };
    worker.onerror = () => {
      if (active) {
        done();
        state.value = 'unavailable';
      }
    };
    worker.postMessage(blob);
  },
  { immediate: true },
);
</script>
<template>
  <p v-if="state === 'loading'">Reading database snapshot…</p>
  <section v-else-if="state === 'ready' && snapshot" aria-label="SQLite physical snapshot">
    <p>
      Standalone database file snapshot. Recent changes in a WAL or journal may be missing. Fields
      show stored records, not SQL column values.
    </p>
    <p v-if="snapshot.walMode">
      This database uses WAL mode; the main file may not contain committed changes.
    </p>
    <p v-if="snapshot.truncated">Only part of the schema was inspected.</p>
    <p v-if="!snapshot.tables.length">No supported tables found.</p>
    <template v-else
      ><label
        >Table
        <select v-model.number="selected">
          <option v-for="(item, i) in snapshot.tables" :key="i" :value="i">{{ item.name }}</option>
        </select></label
      >
      <p v-if="table?.unavailable">{{ table.unavailable }}</p>
      <template v-else-if="table"
        ><p v-if="table.truncated">Showing the first {{ table.rows.length }} stored records.</p>
        <p v-if="!table.rows.length">No stored records.</p>
        <div v-else class="scroll">
          <table>
            <thead>
              <tr>
                <th>Rowid</th>
                <th v-for="i in width" :key="i">Field {{ i }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in table.rows" :key="row.rowid">
                <td>{{ row.rowid }}</td>
                <td v-for="i in width" :key="i">{{ row.fields[i - 1] ?? 'not stored' }}</td>
              </tr>
            </tbody>
          </table>
        </div></template
      >
    </template>
  </section>
  <p v-else>Preview unavailable for this incomplete, large, or unsupported SQLite file.</p>
</template>
<style scoped>
p {
  font-size: 12px;
}
.scroll {
  overflow: auto;
}
table {
  border-collapse: collapse;
}
th,
td {
  border: 1px solid var(--border);
  padding: 6px;
  text-align: left;
  white-space: nowrap;
}
select {
  max-width: 80%;
}
</style>
