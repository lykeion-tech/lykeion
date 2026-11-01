<script setup lang="ts">
import { parseMarkdownBlocks } from '../../lib/markdown';
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useQuery } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import type { RuntimeModel, SkillSummary } from '../../api/types';
import type { SessionMode } from './types';
import { connectorLabel, type ComposerAttachment } from './aristotle';
import ComposerPlusMenu from './cowrite/ComposerPlusMenu.vue';
import ComposerModeStrip from './cowrite/ComposerModeStrip.vue';
import ModelSwitcher from './cowrite/ModelSwitcher.vue';
const props = withDefaults(
  defineProps<{
    modelValue: string;
    busy: boolean;
    blocker?: string;
    error?: string;
    activeRun?: string;
    draft?: boolean;
    mode?: SessionMode;
    template?: string;
    models?: RuntimeModel[];
    selectedModel?: string;
    cli?: string;
    clis?: string[];
    attachments?: ComposerAttachment[];
    initialCapabilities?: { skills: string[]; connectors: string[] };
  }>(),
  {
    mode: 'experiment',
    template: 'blank',
    models: () => [],
    selectedModel: '',
    attachments: () => [],
  },
);
const emit = defineEmits<{
  'update:modelValue': [string];
  send: [{ skills: string[]; connectors: string[] }];
  stop: [];
  'update:mode': [SessionMode];
  'update:template': [string];
  'update:selectedModel': [string];
  'update:cli': [string];
  files: [File[]];
  remove: [string];
  manage: [string];
  capabilities: [{ skills: string[]; connectors: string[] }];
}>();
const plusOpen = ref(false),
  plus = ref<HTMLElement>(),
  input = ref<HTMLInputElement>(),
  textarea = ref<HTMLTextAreaElement>(),
  card = ref<HTMLElement>(),
  room = ref<number>();
const capabilities = ref<{ skills: string[]; connectors: string[] }>({
  skills: [...(props.initialCapabilities?.skills ?? [])],
  connectors: [...(props.initialCapabilities?.connectors ?? [])],
});
watch(
  () => props.initialCapabilities,
  (value) => {
    const next = { skills: [...(value?.skills ?? [])], connectors: [...(value?.connectors ?? [])] };
    if (
      JSON.stringify(next.skills) !== JSON.stringify(capabilities.value.skills) ||
      JSON.stringify(next.connectors) !== JSON.stringify(capabilities.value.connectors)
    )
      capabilities.value = next;
  },
);
watch(
  capabilities,
  (value) => emit('capabilities', { skills: [...value.skills], connectors: [...value.connectors] }),
  { deep: true },
);
function selectCapability(kind: 'skills' | 'connectors', name: string) {
  if (!capabilities.value[kind].includes(name)) capabilities.value[kind].push(name);
}
watch(
  () => props.modelValue,
  (value, old) => {
    if (!value && old && props.busy) capabilities.value = { skills: [], connectors: [] };
  },
);
async function chooseCapability(kind: 'skills' | 'connectors', name: string) {
  if (capabilities.value[kind].includes(name)) return;
  selectCapability(kind, name);
  if (kind === 'skills') {
    const prefix = props.modelValue && !/\s$/.test(props.modelValue) ? ' ' : '';
    emit('update:modelValue', `${props.modelValue}${prefix}/${name} `);
    await nextTick();
    textarea.value?.focus();
  }
}
const highlighted = computed(() => {
  const text = props.modelValue;
  const names = [...capabilities.value.skills].sort((a, b) => b.length - a.length);
  const parts: { text: string; selected: boolean }[] = [];
  let start = 0,
    cursor = 0;
  while (cursor < text.length) {
    const found =
      text[cursor] === '/'
        ? names.find(
            (n) =>
              text.startsWith(`/${n}`, cursor) &&
              (cursor + n.length + 1 === text.length || /\s/.test(text[cursor + n.length + 1]!)),
          )
        : undefined;
    if (!found) {
      cursor++;
      continue;
    }
    if (cursor > start) parts.push({ text: text.slice(start, cursor), selected: false });
    parts.push({ text: `/${found}`, selected: true });
    cursor += found.length + 1;
    start = cursor;
  }
  if (start < text.length) parts.push({ text: text.slice(start), selected: false });
  return parts;
});

const caret = ref(0),
  mentionIndex = ref(0),
  dismissed = ref(false);
const mention = computed(() =>
  dismissed.value ? null : /(?:^|\s)\/([^\s/]*)$/.exec(props.modelValue.slice(0, caret.value)),
);
const skills = useQuery({
  queryKey: computed(() => keys.skills(workspaceId.value)),
  enabled: computed(() => !!workspaceId.value && !!mention.value),
  queryFn: ({ signal }) =>
    api<SkillSummary[]>('GET', '/api/skills', undefined, undefined, {
      workspace: workspaceId.value,
      signal,
    }),
});
const matches = computed(() =>
  (skills.data.value ?? []).filter((s) =>
    s.name.toLowerCase().includes(mention.value?.[1]?.toLowerCase() ?? ''),
  ),
);
const mentionOpen = computed(() => !!mention.value && !!matches.value.length);
const cowrite = computed(() => props.draft && props.mode === 'cowrite');
watch([mentionOpen, () => matches.value.length], async () => {
  await nextTick();
  if (!mentionOpen.value || !card.value?.getBoundingClientRect) return;
  let top = 0;
  for (let node = card.value.parentElement; node; node = node.parentElement) {
    if (['auto', 'scroll', 'hidden'].includes(getComputedStyle(node).overflowY)) {
      top = node.getBoundingClientRect().top;
      break;
    }
  }
  room.value = Math.max(0, Math.round(card.value.getBoundingClientRect().top - top - 8));
});
function outside(e: Event) {
  if (!plus.value?.contains(e.target as Node)) plusOpen.value = false;
}
function escape(e: KeyboardEvent) {
  if (e.key === 'Escape') plusOpen.value = false;
}
function submit() {
  if (!props.busy && !props.blocker && !props.activeRun && props.modelValue.trim())
    emit('send', {
      skills: [...capabilities.value.skills],
      connectors: [...capabilities.value.connectors],
    });
}
function removeSkill(name: string) {
  capabilities.value.skills = capabilities.value.skills.filter((n) => n !== name);
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  emit(
    'update:modelValue',
    props.modelValue.replace(new RegExp(`(^|\\s)/${escaped}(?=\\s|$)`, 'g'), '$1'),
  );
}
function edit(e: Event) {
  const el = e.target as HTMLTextAreaElement;
  capabilities.value.skills = capabilities.value.skills.filter((name) => {
    const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    return new RegExp(`(^|\\s)/${escaped}(?=\\s|$)`).test(el.value);
  });
  emit('update:modelValue', el.value);
  caret.value = el.selectionStart ?? el.value.length;
  mentionIndex.value = 0;
  dismissed.value = false;
  autosize(el);
}
function autosize(el?: HTMLTextAreaElement) {
  if (el?.style) {
    el.style.height = 'auto';
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`;
  }
}
watch(
  () => props.modelValue,
  () => autosize(textarea.value),
  { flush: 'post' },
);
onMounted(() => autosize(textarea.value));
async function insertSkill(name: string) {
  selectCapability('skills', name);
  const start = props.modelValue.slice(0, caret.value).lastIndexOf('/');
  const at = start + name.length + 2;
  emit(
    'update:modelValue',
    `${props.modelValue.slice(0, start)}/${name} ${props.modelValue.slice(caret.value)}`,
  );
  dismissed.value = true;
  await nextTick();
  textarea.value?.focus();
  textarea.value?.setSelectionRange?.(at, at);
  caret.value = at;
}
function keydown(e: KeyboardEvent) {
  if (e.isComposing) return;
  if (mentionOpen.value) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      mentionIndex.value =
        (mentionIndex.value + (e.key === 'ArrowDown' ? 1 : matches.value.length - 1)) %
        matches.value.length;
      return;
    }
    if (e.key === 'Enter' || e.key === 'Tab') {
      e.preventDefault();
      void insertSkill(matches.value[Math.min(mentionIndex.value, matches.value.length - 1)]!.name);
      return;
    }
    if (e.key === 'Escape') {
      e.preventDefault();
      dismissed.value = true;
      return;
    }
  }
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault();
    submit();
  }
}
function pick(e: Event) {
  const el = e.target as HTMLInputElement;
  const files = Array.from(el.files ?? []);
  el.value = '';
  if (files.length) emit('files', files);
}
onMounted(() => {
  document.addEventListener('pointerdown', outside);
  document.addEventListener('keydown', escape);
});
onUnmounted(() => {
  document.removeEventListener('pointerdown', outside);
  document.removeEventListener('keydown', escape);
});
const inline = (text?: string) =>
  parseMarkdownBlocks(text ?? '')
    .filter((t) => t.type === 'inline')
    .map((t) => t.html)
    .join('')
    .replace(/<\/?a\b[^>]*>/g, '');
</script>
<template>
  <div class="aristotle-composer">
    <div
      ref="card"
      class="composer-card"
      :class="draft ? 'composer-card--hero' : 'composer-card--docked'"
      :style="room === undefined ? undefined : { '--mention-ceiling': `${room}px` }"
    >
      <div v-if="blocker" class="composer-blocker" role="status">
        {{ blocker }} <RouterLink to="/settings?tab=machines">Settings</RouterLink>
      </div>
      <div v-if="attachments.length" class="composer-attachments">
        <span v-for="file in attachments" :key="file.upload_id" class="composer-attachment"
          ><span class="composer-attachment-name">{{ file.name }}</span
          ><button
            type="button"
            class="composer-chip-x"
            :aria-label="`Remove ${file.name}`"
            :title="`Remove ${file.name}`"
            :disabled="busy"
            @click="emit('remove', file.upload_id)"
          >
            ×
          </button></span
        >
      </div>
      <div
        v-if="capabilities.skills.length || capabilities.connectors.length"
        class="composer-attachments"
      >
        <span
          v-for="name in capabilities.skills"
          :key="'s' + name"
          class="composer-attachment composer-capability"
          >{{ name
          }}<button
            class="composer-chip-x"
            type="button"
            :aria-label="`Remove ${name}`"
            @click="removeSkill(name)"
          >
            ×
          </button></span
        >
        <span
          v-for="name in capabilities.connectors"
          :key="'c' + name"
          class="composer-attachment composer-capability"
          >{{ connectorLabel(name)
          }}<button
            class="composer-chip-x"
            type="button"
            :aria-label="`Remove ${connectorLabel(name)}`"
            @click="capabilities.connectors = capabilities.connectors.filter((n) => n !== name)"
          >
            ×
          </button></span
        >
      </div>
      <p v-if="error" class="composer-attach-refusal" role="alert">{{ error }}</p>
      <input
        ref="input"
        type="file"
        multiple
        hidden
        aria-label="Files"
        :disabled="busy"
        @change="pick"
      />
      <div class="composer-input-wrap">
        <div v-if="modelValue" class="composer-input-mirror" aria-hidden="true">
          <template v-for="(part, index) in highlighted" :key="index"
            ><span v-if="part.selected" class="composer-selection">{{ part.text }}</span
            ><template v-else>{{ part.text }}</template></template
          >{{ '\n' }}
        </div>
        <textarea
          ref="textarea"
          class="composer-textarea"
          :value="modelValue"
          aria-label="Message the agent"
          :placeholder="
            cowrite ? 'What is the paper about?' : 'What are you running? — / for skills'
          "
          :rows="draft ? 2 : 1"
          :disabled="busy"
          @input="edit"
          @select="
            caret = ($event.target as HTMLTextAreaElement).selectionStart;
            dismissed = false;
          "
          @keydown="keydown"
          @scroll="
            ($event.target as HTMLTextAreaElement).previousElementSibling &&
            (($event.target as HTMLTextAreaElement).previousElementSibling!.scrollTop = (
              $event.target as HTMLTextAreaElement
            ).scrollTop)
          "
        />
      </div>
      <div
        v-if="mentionOpen"
        class="mention-menu"
        role="listbox"
        aria-label="Skills"
        data-testid="mention-menu"
      >
        <div class="mention-menu-head">Skills</div>
        <button
          v-for="(skill, index) in matches"
          :key="skill.id"
          type="button"
          role="option"
          :aria-selected="index === Math.min(mentionIndex, matches.length - 1)"
          class="mention-item"
          :class="{ 'is-active': index === Math.min(mentionIndex, matches.length - 1) }"
          :title="skill.description || undefined"
          @mousedown.prevent="insertSkill(skill.name)"
        >
          <span class="mention-item-label">{{ skill.name }}</span
          ><span
            v-if="skill.description"
            class="md-inline mention-item-desc"
            v-html="inline(skill.description)"
          />
        </button>
      </div>
      <div class="composer-toolbar">
        <div class="composer-tools">
          <div ref="plus" class="composer-plus">
            <button
              type="button"
              class="composer-icon-btn composer-plus-btn"
              title="Files, skills, connectors, settings"
              aria-label="Add"
              aria-haspopup="menu"
              :aria-expanded="plusOpen"
              :disabled="busy"
              @click="plusOpen = !plusOpen"
            >
              <svg
                width="16"
                height="16"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="1.75"
                stroke-linecap="round"
                stroke-linejoin="round"
                aria-hidden="true"
              >
                <path d="M12 5v14M5 12h14" />
              </svg>
            </button>
            <ComposerPlusMenu
              v-if="plusOpen"
              :cowrite="cowrite"
              :template="template"
              @update:template="emit('update:template', $event)"
              @select="chooseCapability"
              @attach="input?.click()"
              @close="plusOpen = false"
              @manage="emit('manage', $event)"
            />
          </div>
          <ComposerModeStrip
            v-if="draft"
            :model-value="mode"
            @update:model-value="emit('update:mode', $event)"
          />
        </div>
        <div class="composer-tools">
          <ModelSwitcher
            :models="models"
            :cli="cli"
            :clis="clis"
            :model-value="selectedModel"
            :disabled="!draft || busy"
            @update:model-value="emit('update:selectedModel', $event)"
            @update:cli="emit('update:cli', $event)"
          />
          <button
            v-if="activeRun"
            type="button"
            class="composer-send composer-send--stop"
            aria-label="Stop"
            title="Stop"
            :disabled="busy"
            @click="emit('stop')"
          >
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
              <rect x="4.75" y="4.75" width="6.5" height="6.5" rx="1.5" fill="currentColor" />
            </svg>
          </button>
          <button
            v-else
            type="button"
            class="composer-send"
            :aria-label="cowrite ? 'Start paper' : 'Send'"
            :title="cowrite ? 'Start paper' : 'Send'"
            :disabled="busy || !!blocker || !modelValue.trim()"
            @click="submit"
          >
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
              <path
                d="M8 13V3.5M8 3.5 4.5 7M8 3.5 11.5 7"
                stroke="currentColor"
                stroke-width="1.5"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
            </svg>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
<style scoped>
.aristotle-composer {
  width: min(896px, calc(100% - 48px));
  margin: 12px auto;
  flex: none;
}
</style>
