<script setup lang="ts">
import { computed, reactive, ref } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import type { DecisionItem, DecisionQuestion, DecisionQuestionAnswer } from '../../api/types';

const props = defineProps<{ item: DecisionItem }>();
const client = useQueryClient();
const busy = ref(false),
  error = ref('');
const choices = reactive<Record<string, string[]>>({});
const custom = reactive<Record<string, boolean>>({});
const text = reactive<Record<string, string>>({});
const questions = computed(() => props.item.question_set?.questions ?? []);
const answerable = computed(() => props.item.can_answer && props.item.status === 'open');
const answered = (q: DecisionQuestion) =>
  q.mode === 'text' || custom[q.id] ? !!text[q.id]?.trim() : !!choices[q.id]?.length;
const valid = computed(() =>
  questions.value.every((q) => {
    if ((q.mode === 'text' || custom[q.id]) && Array.from(text[q.id] ?? '').length > 100000)
      return false;
    if (custom[q.id] && !text[q.id]?.trim()) return false;
    return !q.required || answered(q);
  }),
);
function select(q: DecisionQuestion, id: string) {
  if (!answerable.value || busy.value) return;
  const alreadySelected = !custom[q.id] && (choices[q.id] ?? []).includes(id);
  custom[q.id] = false;
  if (q.mode === 'single') choices[q.id] = !q.required && alreadySelected ? [] : [id];
  else
    choices[q.id] = (choices[q.id] ?? []).includes(id)
      ? choices[q.id].filter((x) => x !== id)
      : [...(choices[q.id] ?? []), id];
}
function selectOther(q: DecisionQuestion) {
  if (!answerable.value || busy.value) return;
  custom[q.id] = !q.required && custom[q.id] ? false : true;
  choices[q.id] = [];
}
function history(q: DecisionQuestion) {
  const a = props.item.answers?.find((v) => v.question_id === q.id);
  if (!a) return 'Not answered';
  if (a.text !== null) return a.text;
  return (
    a.option_ids.map((id) => q.options.find((o) => o.id === id)?.label ?? id).join(', ') ||
    'Not answered'
  );
}
async function submit() {
  if (!answerable.value || !valid.value || busy.value) return;
  const answers: DecisionQuestionAnswer[] = [];
  for (const q of questions.value) {
    if (!answered(q)) continue;
    answers.push({
      question_id: q.id,
      option_ids: q.mode === 'text' || custom[q.id] ? [] : (choices[q.id] ?? []),
      text: q.mode === 'text' || custom[q.id] ? text[q.id] : null,
    });
  }
  busy.value = true;
  error.value = '';
  try {
    await api('POST', `/api/decisions/${props.item.id}/answers`, { answers });
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.decisionsAll(props.item.workspace_id) }),
      client.invalidateQueries({ queryKey: keys.decisionCount(props.item.workspace_id) }),
    ]);
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <div class="question-set">
    <p v-if="item.status === 'open' && !item.can_answer" class="permission-note">
      Only the study owner or a workspace owner or admin can answer this decision.
    </p>
    <section
      v-for="(q, index) in questions"
      :key="q.id"
      class="question"
      :aria-label="`Question ${index + 1}`"
    >
      <div class="question-header">
        <span>QUESTION {{ index + 1 }}</span
        ><span>{{ q.required ? 'Required' : 'Optional' }}</span>
      </div>
      <h2>{{ q.prompt }}</h2>
      <p v-if="q.help_text" class="help">{{ q.help_text }}</p>
      <p v-if="q.mode !== 'text'" class="choice-instruction">{{ q.mode === 'single' ? 'Choose one' : 'Choose one or more' }}</p>
      <template v-if="item.status === 'open'">
        <textarea
          v-if="q.mode === 'text'"
          class="answer-text"
          :aria-label="q.prompt"
          :required="q.required"
          :disabled="!answerable || busy"
          :value="text[q.id] ?? ''"
          @input="text[q.id] = ($event.target as HTMLTextAreaElement).value"
        />
        <div v-else class="choices" role="group" :aria-label="q.prompt">
          <button
            v-for="option in q.options"
            :key="option.id"
            type="button"
            class="choice"
            :class="{ selected: choices[q.id]?.includes(option.id) && !custom[q.id] }"
            :disabled="!answerable || busy"
            :aria-pressed="!!choices[q.id]?.includes(option.id) && !custom[q.id]"
            @click="select(q, option.id)"
          >
            <b>{{ option.label }}</b
            ><small>{{ option.consequence }}</small>
          </button>
          <button
            v-if="q.allow_other"
            type="button"
            class="choice"
            :class="{ selected: custom[q.id] }"
            :disabled="!answerable || busy"
            :aria-pressed="!!custom[q.id]"
            @click="selectOther(q)"
          >
            <b>Something else</b>
          </button>
          <textarea
            v-if="custom[q.id]"
            class="answer-text"
            :aria-label="`Custom answer for ${q.prompt}`"
            :disabled="!answerable || busy"
            :value="text[q.id] ?? ''"
            @input="text[q.id] = ($event.target as HTMLTextAreaElement).value"
          />
        </div>
      </template>
      <p v-else class="history-answer">
        {{ item.status === 'decided' ? history(q) : 'Not answered' }}
      </p>
    </section>
    <p
      v-if="
        questions.some(
          (q) =>
            (q.mode === 'text' || custom[q.id]) && Array.from(text[q.id] ?? '').length > 100000,
        )
      "
      role="alert"
      class="inline-error"
    >
      An answer cannot exceed 100,000 characters.
    </p>
    <p v-if="error" role="alert" class="inline-error">{{ error }}</p>
    <button
      v-if="answerable"
      type="button"
      class="button primary submit"
      :disabled="!valid || busy"
      @click="submit"
    >
      {{ busy ? 'Submitting…' : 'Submit' }}
    </button>
  </div>
</template>
<style scoped>
.question-set {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.permission-note,
.choice-instruction,
.help,
.history-answer {
  color: var(--muted);
  line-height: 1.55;
}
.permission-note,
.choice-instruction {
  font-size: 13px;
}
.choice-instruction { margin-top: 8px; }
.question {
  padding: 18px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
}
.question-header {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  color: var(--muted);
  font-size: 13px;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}
h2 {
  margin: 10px 0 0;
  font-size: 14px;
  line-height: 1.5;
}
.help {
  margin: 6px 0 0;
  font-size: 13px;
  white-space: pre-wrap;
}
.choices {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 14px;
}
.choice {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 5px;
  width: 100%;
  padding: 13px 16px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--surface);
  color: var(--text);
  text-align: left;
  cursor: pointer;
}
.choice small {
  color: var(--muted);
  font-size: 13px;
  line-height: 1.5;
  font-weight: 400;
}
.choice:hover:not(:disabled) {
  border-color: var(--primary);
}
.choice.selected {
  border-color: var(--primary);
  background: color-mix(in srgb, var(--primary) 14%, var(--surface));
}
.choice:disabled {
  cursor: default;
  opacity: 0.75;
}
.choice:focus-visible,
.submit:focus-visible,
.answer-text:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 2px;
}
.answer-text {
  display: block;
  width: 100%;
  min-height: 88px;
  margin-top: 14px;
  padding: 10px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--surface);
  color: inherit;
  font: inherit;
  resize: vertical;
}
.history-answer {
  margin-top: 12px;
  white-space: pre-wrap;
}
.submit {
  align-self: flex-start;
}
</style>
