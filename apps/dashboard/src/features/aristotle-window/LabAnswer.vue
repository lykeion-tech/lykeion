<script setup lang="ts">
import { computed, ref } from 'vue';
import { Flag } from '@lucide/vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { workspaceId } from '../../api/session';
import { labKey, sourcePath, type LabMessage, type LabRef } from './api';
const props = defineProps<{ message: LabMessage; evidence: boolean }>();
const evidenceSources = computed(() => {
  const seen = new Set<string>();
  return props.message.evidence.filter((item) => {
    const key = sourcePath(item) ?? item.ref;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
});
const flagging = ref(''),
  reason = ref(''),
  busy = ref(false),
  error = ref('');
const client = useQueryClient();
function openFlag(cite: LabRef) {
  flagging.value = cite.ref;
  reason.value = '';
  error.value = '';
}
async function submit() {
  if (busy.value) return;
  busy.value = true;
  const ws = workspaceId.value;
  try {
    await api(
      'POST',
      '/api/lessons/' + encodeURIComponent(flagging.value.slice(7)) + '/flag',
      { reason: reason.value },
      undefined,
      { workspace: ws },
    );
    flagging.value = '';
    await client.invalidateQueries({ queryKey: labKey(ws) });
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <article class="answer">
    <div
      v-for="(statement, i) in message.statements"
      :key="i"
      class="statement"
    >
      <span v-if="statement.kind === 'lesson'" class="tag lesson">Lesson</span>{{ statement.text }}
      <template v-for="cite in statement.cites.filter((source) => !source.ref.startsWith('study:'))" :key="cite.ref">
        <RouterLink v-if="sourcePath(cite)" class="cite" :to="sourcePath(cite)!">{{
          cite.title
        }}</RouterLink>
        <template v-else-if="cite.ref.startsWith('lesson:')">
          <details class="lesson">
            <summary class="cite">{{ cite.title }}</summary>
            <div class="facts">
              <span>Backing facts</span>
              <template v-for="fact in cite.facts" :key="fact.ref"
                ><RouterLink v-if="sourcePath(fact)" :to="sourcePath(fact)!">{{
                  fact.title
                }}</RouterLink
                ><span v-else>{{ fact.title }}</span></template
              >
            </div>
          </details>
          <button
            class="flag"
            aria-label="Flag lesson"
            title="Flag lesson"
            :disabled="busy"
            @click="openFlag(cite)"
          >
            <Flag :size="13" />
          </button>
          <form v-if="flagging === cite.ref" @submit.prevent="submit">
            <input
              v-model="reason"
              aria-label="Flag reason"
              placeholder="Reason (optional)"
              maxlength="500"
            />
            <button type="button" :disabled="busy" @click="submit">Submit flag</button>
            <button type="button" :disabled="busy" @click="flagging = ''">Cancel</button>
            <span v-if="error" role="alert">{{ error }}</span>
          </form>
        </template>
        <span v-else class="cite">{{ cite.title }}</span>
      </template>
    </div>
    <section v-if="evidence" class="evidence">
      <strong>Evidence</strong>
      <p v-if="!message.evidence.length">No records were read for this answer.</p>
      <div v-for="item in evidenceSources" :key="sourcePath(item) ?? item.ref" class="source">
        <RouterLink v-if="sourcePath(item)" :to="sourcePath(item)!">{{ item.title }}</RouterLink
        ><span v-else>{{ item.title }}</span>
      </div>
    </section>
  </article>
</template>
<style scoped>
.answer {
  line-height: 1.55;
  overflow-wrap: anywhere;
}
.statement + .statement {
  margin-top: 7px;
}
.tag,
.cite {
  font-size: 11px;
  border-radius: 4px;
  padding: 1px 5px;
}
.tag {
  font-weight: 600;
  margin-right: 4px;
}
.tag.lesson {
  background: color-mix(in srgb, #d99a1e 20%, var(--surface));
  color: color-mix(in srgb, #c48a12 70%, var(--text));
}
.cite {
  margin-left: 3px;
  background: color-mix(in srgb, var(--primary) 16%, var(--surface));
  cursor: pointer;
}
a,
.cite,
.flag {
  color: var(--primary-text);
}
details.lesson {
  display: inline;
}
.facts,
.evidence {
  padding: 8px 10px;
  border-radius: 8px;
  background: var(--input);
  margin-block: 6px;
}
.facts,
.source {
  display: grid;
  gap: 3px;
}
.source {
  margin-top: 5px;
}
button,
input {
  font: inherit;
  color: inherit;
}
.flag {
  vertical-align: middle;
  border: 0;
  background: none;
  cursor: pointer;
}
form {
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
  margin: 6px 0;
}
input {
  min-width: 0;
  flex: 1;
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 5px;
}
form button {
  border: 1px solid var(--border);
  background: var(--surface);
  border-radius: 6px;
  padding: 4px 7px;
}
small,
.facts span,
.evidence p {
  color: var(--muted);
}
</style>
