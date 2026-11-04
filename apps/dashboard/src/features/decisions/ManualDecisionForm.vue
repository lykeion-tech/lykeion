<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useCards } from '../../api/queries';
import { manualDecisionDraftError, type ManualDecisionDraft } from './manualDecisionDraft';
const props = withDefaults(
  defineProps<{
    modelValue: ManualDecisionDraft;
    busy?: boolean;
    readonly?: boolean;
    submitDisabled?: boolean;
    submitLabel?: string;
  }>(),
  { submitLabel: 'Create' },
);
const emit = defineEmits<{ 'update:modelValue': [value: ManualDecisionDraft]; submit: [] }>();
const cards = useCards();
const titleInput = ref<HTMLInputElement>();
const error = computed(() => manualDecisionDraftError(props.modelValue));
function change(key: keyof ManualDecisionDraft, value: string) {
  emit('update:modelValue', {
    ...props.modelValue,
    [key]: key === 'deadline' || key === 'card_id' ? value || null : value,
  });
}
onMounted(() => titleInput.value?.focus?.());
</script>
<template>
  <form class="manual-form" @submit.prevent="!error && !busy && !readonly && !submitDisabled && emit('submit')">
    <label
      >Title<input
        ref="titleInput"
        :value="modelValue.title"
        aria-label="Title"
        required
        autocomplete="off"
        :disabled="busy || readonly"
        @input="change('title', ($event.target as HTMLInputElement).value)"
    /></label>
    <label
      >Context <span>Optional</span
      ><textarea
        :value="modelValue.context"
        aria-label="Context"
        rows="5"
        :disabled="busy || readonly"
        @input="change('context', ($event.target as HTMLTextAreaElement).value)"
      />
    </label>
    <div class="form-row">
      <label
        >Deadline <span>Optional</span
        ><input
          type="date"
          :value="modelValue.deadline ?? ''"
          aria-label="Deadline"
          :disabled="busy || readonly"
          @input="change('deadline', ($event.target as HTMLInputElement).value)"
      /></label>
      <label
        >Study <span>Optional</span
        ><select
          :value="modelValue.card_id ?? ''"
          aria-label="Study"
          :disabled="busy || readonly"
          @change="change('card_id', ($event.target as HTMLSelectElement).value)"
        >
          <option value="">No study</option>
          <option v-for="card in cards.data.value" :key="card.id" :value="card.id">
            {{ card.title }}
          </option>
        </select></label
      >
    </div>
    <p class="hint">
      Only you can see this note. A deadline sends a reminder; the note stays open until you resolve
      or dismiss it.
    </p>
    <p v-if="error && modelValue.title" role="alert" class="inline-error">{{ error }}</p>
    <div class="form-actions">
      <slot /><button
        v-if="!readonly"
        type="submit"
        class="button primary"
        :disabled="busy || submitDisabled || !!error"
      >
        {{ busy ? 'Saving…' : submitLabel }}
      </button>
    </div>
  </form>
</template>
<style scoped>
.manual-form {
  display: flex;
  flex-direction: column;
  gap: 20px;
  max-width: 680px;
  width: 100%;
}
label {
  display: flex;
  flex-direction: column;
  gap: 8px;
  font-size: 13px;
  font-weight: 500;
  min-width: 0;
}
label span,
.hint {
  color: var(--muted);
  font-size: 12px;
  font-weight: 400;
}
label span {
  display: inline;
}
input,
textarea,
select {
  width: 100%;
  box-sizing: border-box;
  padding: 10px 12px;
  background: var(--surface);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 6px;
  font: inherit;
}
textarea {
  resize: vertical;
  line-height: 1.6;
}
input:focus-visible,
textarea:focus-visible,
select:focus-visible,
button:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 2px;
}
.form-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}
.hint {
  line-height: 1.6;
}
.form-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
}
@media (max-width: 600px) {
  .form-row {
    grid-template-columns: 1fr;
  }
}
</style>
