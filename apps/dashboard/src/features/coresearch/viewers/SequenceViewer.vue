<script setup lang="ts">
import { computed } from 'vue';
import { sequencePreview } from './scientific-text';
const props = defineProps<{ text: string; extension: string; partial: boolean }>();
const preview = computed(() => sequencePreview(props.text, props.extension, props.partial));
function color(base: string) {
  return (
    ({ A: 'a', C: 'c', G: 'g', T: 't', U: 't' } as Record<string, string>)[base.toUpperCase()] ??
    'other'
  );
}
</script>

<template>
  <section v-if="preview" class="sequences" aria-label="Sequence preview">
    <p v-if="preview.partial" class="sequence-note">Showing a partial preview.</p>
    <article v-for="(record, index) in preview.records" :key="index" class="sequence-record">
      <h3>{{ record.name }}</h3>
      <p class="sequence-note">
        {{ record.length }} {{ record.gc !== undefined ? 'bases' : 'residues'
        }}<span v-if="record.gc !== undefined">
          · GC {{ record.gc.toFixed(1) }}% of called bases</span
        ><span v-if="record.quality !== undefined">
          · mean quality {{ record.quality.toFixed(1) }} (Phred+33)</span
        >
      </p>
      <div class="sequence-bases">
        <span
          v-for="(base, i) in record.sequence"
          :key="i"
          :class="record.gc !== undefined ? `base-${color(base)}` : undefined"
          >{{ base }}</span
        ><span v-if="record.length > record.sequence.length">…</span>
      </div>
    </article>
  </section>
  <p v-else class="sequence-note">
    Sequence preview unavailable. The file may be incomplete or malformed.
  </p>
</template>

<style scoped>
.sequences {
  display: grid;
  gap: 12px;
  min-width: 0;
}
.sequence-record {
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 12px;
  min-width: 0;
}
h3 {
  margin: 0;
  font-size: 13px;
  overflow-wrap: anywhere;
}
.sequence-note {
  color: var(--muted);
  font-size: 13px;
  margin: 6px 0;
}
.sequence-bases {
  overflow-wrap: anywhere;
  font:
    12px/1.8 ui-monospace,
    monospace;
}
.base-a {
  color: #21805f;
}
.base-c {
  color: #286bb2;
}
.base-g {
  color: #aa6b00;
}
.base-t {
  color: #bc4056;
}
</style>
