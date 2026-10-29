<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, nextTick, watch } from 'vue';
import { MANUSCRIPT_LABELS } from './lib/manuscript';
export interface SelectionTarget {
  rect: { top: number; left: number; width: number; height: number };
  from: number;
  to: number;
  quote: string;
  path: string;
  line: number;
}
const props = withDefaults(
  defineProps<{
    target: SelectionTarget;
    owner: boolean;
    people?: { id: string; name: string }[];
  }>(),
  { people: () => [] },
);
const emit = defineEmits<{
  proofread: [SelectionTarget];
  edit: [SelectionTarget, string];
  comment: [SelectionTarget, string, string[]];
  close: [];
}>();
const mode = ref<'tools' | 'comment' | 'edit'>('tools'),
  text = ref(''),
  caret = ref(0),
  holdsFocus = ref(false),
  box = ref<HTMLElement>(),
  field = ref<HTMLInputElement>();
const mentioned = new Map<string, string>();
const atQuery = computed(() =>
  mode.value === 'comment'
    ? /(?:^|\s)@([^\s@]*)$/.exec(text.value.slice(0, caret.value))?.[1]
    : undefined,
);
const matches = computed(() =>
  atQuery.value === undefined
    ? []
    : props.people
        .filter((p) => p.name.toLowerCase().includes(atQuery.value!.toLowerCase()))
        .slice(0, 6),
);
async function pick(person: { id: string; name: string }) {
  const before = text.value.slice(0, caret.value).replace(/@([^\s@]*)$/, `@${person.name} `);
  text.value = before + text.value.slice(caret.value);
  mentioned.set(person.name, person.id);
  await nextTick();
  field.value?.focus();
  field.value?.setSelectionRange(before.length, before.length);
  caret.value = before.length;
}
function proofread() {
  if (!props.owner) return;
  emit('proofread', props.target);
  emit('close');
}
function submit() {
  const said = text.value.trim();
  if (!said) return;
  if (mode.value === 'comment')
    emit('comment', props.target, said, [
      ...new Set([...mentioned].filter(([name]) => said.includes(`@${name}`)).map(([, id]) => id)),
    ]);
  else if (props.owner) emit('edit', props.target, said);
  emit('close');
}
function key(e: KeyboardEvent) {
  if (e.key !== 'Escape') return;
  e.preventDefault();
  if (mode.value === 'tools') emit('close');
  else {
    mode.value = 'tools';
    text.value = '';
  }
}
function barKey(e: KeyboardEvent) {
  if (mode.value !== 'tools' || e.metaKey || e.ctrlKey || e.altKey) return;
  if (e.key === 'p' && props.owner) {
    e.preventDefault();
    proofread();
  } else if (e.key === 'c') {
    e.preventDefault();
    mode.value = 'comment';
  } else if (e.key === 'e' && props.owner) {
    e.preventDefault();
    mode.value = 'edit';
  }
}
const gone = () => emit('close');
const outside = (e: Event) => {
  if (!box.value?.contains(e.target as Node)) gone();
};
watch(mode, async () => {
  await nextTick();
  if (mode.value !== 'tools') field.value?.focus();
});
onMounted(() => {
  box.value?.showPopover?.();
  window.addEventListener('scroll', gone, true);
  window.addEventListener('resize', gone);
  window.addEventListener('keydown', key);
  window.addEventListener('pointerdown', outside);
});
onBeforeUnmount(() => {
  window.removeEventListener('scroll', gone, true);
  window.removeEventListener('resize', gone);
  window.removeEventListener('keydown', key);
  window.removeEventListener('pointerdown', outside);
});
const quote = computed(() =>
  props.target.quote.length > 60 ? `${props.target.quote.slice(0, 60)}…` : props.target.quote,
);
const hint = (name: string, key: string) => (holdsFocus.value ? `${name} (${key})` : name);
</script>
<template>
  <div
    ref="box"
    popover="auto"
    class="ms-sel"
    :class="`ms-sel--${mode}`"
    :style="{
      top: `${target.rect.top - 48}px`,
      left: `${target.rect.left + target.rect.width / 2}px`,
    }"
    data-testid="ms-sel"
    @keydown="barKey"
    @focusin="holdsFocus = true"
    @focusout="!box?.contains($event.relatedTarget as Node) && (holdsFocus = false)"
  >
    <div
      v-if="mode === 'tools'"
      class="ms-sel-tools"
      role="toolbar"
      aria-label="Ask about the passage"
    >
      <button
        v-if="owner"
        type="button"
        class="ms-sel-tool"
        :title="hint('Proofread', 'p')"
        @click="proofread"
      >
        Proofread</button
      ><button
        type="button"
        class="ms-sel-tool"
        :title="hint('Comment', 'c')"
        @click="mode = 'comment'"
      >
        Comment</button
      ><button
        v-if="owner"
        type="button"
        class="ms-sel-tool"
        :title="hint('Edit with AI', 'e')"
        @click="mode = 'edit'"
      >
        Edit with AI
      </button>
    </div>
    <div v-else class="ms-sel-editor">
      <div class="ms-sel-quote">“{{ quote }}”</div>
      <div v-if="mode === 'comment'" class="ms-sel-chips">
        <button
          v-for="label in MANUSCRIPT_LABELS"
          :key="label.id"
          type="button"
          class="ms-sel-chip"
          @click="text = text.trim() ? `${text.trim()} ${label.sentence}` : label.sentence"
        >
          {{ label.text }}
        </button>
      </div>
      <input
        ref="field"
        v-model="text"
        class="ms-sel-field"
        type="text"
        :aria-label="mode === 'comment' ? 'Comment on the passage' : 'Edit this passage with AI'"
        :placeholder="mode === 'comment' ? 'What about it?' : 'What should change?'"
        @input="caret = ($event.target as HTMLInputElement).selectionStart ?? text.length"
        @select="caret = ($event.target as HTMLInputElement).selectionStart ?? 0"
        @keydown.enter.exact.prevent="
          !$event.isComposing && (matches.length ? pick(matches[0]!) : submit())
        "
      />
      <div
        v-if="matches.length"
        class="ms-sel-mentions"
        role="listbox"
        aria-label="Mention a member"
      >
        <button
          v-for="person in matches"
          :key="person.id"
          type="button"
          role="option"
          :aria-selected="false"
          class="ms-sel-mention"
          @mousedown.prevent="pick(person)"
        >
          {{ person.name }}
        </button>
      </div>
    </div>
  </div>
</template>
