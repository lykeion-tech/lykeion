<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import Modal from './Modal.vue';
import Icon from './Icon.vue';
import { useCards, useResearches, useWorkspace } from '../api/queries';
import { workspaceId } from '../api/session';
import {
  paletteEntries,
  paletteSelection,
  rankPaletteEntries,
  type PaletteEntry,
} from '../lib/search';
import { columnName, timeAgo } from '../lib/studies';

const emit = defineEmits<{ close: [] }>();
const router = useRouter();
const studies = useCards();
const researches = useResearches();
const workspace = useWorkspace();
const query = ref('');
const active = ref(0);
const list = ref<HTMLElement>();
const entries = computed(() =>
  paletteEntries(
    researches.data.value ?? [],
    studies.data.value ?? [],
    workspace.data.value?.display_prefix ?? '',
  ),
);
const results = computed(() => rankPaletteEntries(entries.value, query.value));
const selected = computed(() => results.value[active.value]);
const loading = computed(() => studies.isPending.value || researches.isPending.value);
const failure = computed(() => studies.error.value || researches.error.value);
watch(workspaceId, () => emit('close'), { flush: 'sync' });
let rankedQuery = query.value;
let moved = false;
watch(results, (next, previous) => {
  if (query.value !== rankedQuery) moved = false;
  rankedQuery = query.value;
  active.value = paletteSelection(previous, next, active.value, moved);
});
function select(index: number) {
  moved = true;
  active.value = index;
}
watch(active, async () => {
  await nextTick();
  list.value?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' });
});
function parts(label: string) {
  const text = query.value.trim();
  const index = text ? label.toLowerCase().indexOf(text.toLowerCase()) : -1;
  return index < 0
    ? { before: label, match: '', after: '' }
    : {
        before: label.slice(0, index),
        match: label.slice(index, index + text.length),
        after: label.slice(index + text.length),
      };
}
async function open(entry?: PaletteEntry) {
  if (!entry) return;
  if (router.currentRoute.value.path === entry.path) {
    emit('close');
    return;
  }
  const result = await router.push(entry.path);
  if (!result) emit('close');
}
function key(event: KeyboardEvent) {
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault();
    select(
      Math.max(
        0,
        Math.min(results.value.length - 1, active.value + (event.key === 'ArrowDown' ? 1 : -1)),
      ),
    );
  } else if (event.key === 'Enter' && !event.isComposing) {
    event.preventDefault();
    void open(selected.value);
  } else if (event.key === 'Tab') {
    event.preventDefault();
  }
}
</script>

<template>
  <Modal title="Command palette" class="command-palette" @close="emit('close')">
    <div @keydown="key">
      <input
        v-model="query"
        class="palette-input"
        autofocus
        role="combobox"
        aria-label="Search Researches and Studies"
        aria-expanded="true"
        aria-controls="palette-results"
        :aria-activedescendant="selected ? 'palette-' + selected.id : undefined"
        aria-autocomplete="list"
        placeholder="Type a command or search…"
        :spellcheck="false"
      />
      <div class="palette-split">
        <ul
          id="palette-results"
          ref="list"
          class="palette-results"
          role="listbox"
          aria-label="Researches and Studies"
        >
          <li v-if="failure" class="palette-message" role="presentation">
            <span role="alert">Could not load search results. {{ failure.message }}</span>
          </li>
          <li v-else-if="loading" class="palette-message" role="presentation">
            <span role="status">Loading…</span>
          </li>
          <li v-else-if="!results.length" class="palette-message" role="presentation">
            No matching results
          </li>
          <li
            v-for="(entry, index) in results"
            :id="'palette-' + entry.id"
            :key="entry.id"
            role="option"
            :aria-selected="active === index"
            class="palette-option"
            @mouseenter="select(index)"
            @mousedown.prevent
            @click="open(entry)"
          >
            <span class="palette-label"
              >{{ parts(entry.label).before
              }}<span class="palette-match">{{ parts(entry.label).match }}</span
              >{{ parts(entry.label).after }}</span
            >
            <Icon
              :name="entry.kind === 'research' ? 'flask' : 'tasks'"
              :size="13"
              aria-hidden="true"
            />
            <span class="sr-only">{{ entry.kind === 'research' ? 'Research' : 'Study' }}</span>
          </li>
        </ul>
        <div v-if="selected" class="palette-preview" aria-hidden="true">
          <div class="palette-preview-title">{{ selected.title }}</div>
          <div class="palette-preview-subtitle">{{ selected.subtitle }}</div>
          <div v-if="selected.research" class="palette-preview-row">
            <span>Research</span><span>{{ selected.research }}</span>
          </div>
          <div v-if="selected.stage" class="palette-preview-row">
            <span>Stage</span><span>{{ columnName(selected.stage) }}</span>
          </div>
          <div v-if="selected.state" class="palette-preview-row">
            <span>Status</span><span class="palette-state">{{ selected.state }}</span>
          </div>
          <div class="palette-preview-row">
            <span>Updated</span><span>{{ timeAgo(selected.updated) }}</span>
          </div>
        </div>
      </div>
    </div>
  </Modal>
</template>

<style scoped>
.command-palette {
  width: min(720px, calc(100vw - 32px));
  max-width: none;
  margin: 12vh auto 0;
  padding: 0;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--modal);
  box-shadow: 0 18px 60px #0008;
  overflow: hidden;
}
.command-palette::backdrop {
  background: #0009;
  backdrop-filter: none;
}
.palette-input {
  display: block;
  width: 100%;
  height: 46px;
  padding: 0 16px;
  border: 0;
  border-bottom: 1px solid var(--border);
  border-radius: 0;
  background: transparent;
  color: var(--text);
  font: inherit;
  font-size: 14px;
  outline: none;
}
.palette-input::placeholder {
  color: var(--muted);
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
.palette-split {
  display: flex;
  align-items: stretch;
}
.palette-results {
  list-style: none;
  flex: 1;
  min-width: 0;
  max-height: 320px;
  overflow-y: auto;
  padding: 6px;
  margin: 0;
}
.palette-option {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 34px;
  padding: 0 10px;
  border-radius: 6px;
  font-size: 14px;
  color: var(--muted);
  cursor: pointer;
}
.palette-option[aria-selected='true'] {
  background: var(--hover);
  color: var(--text);
}
.palette-label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.palette-option .icon {
  flex: none;
  color: var(--muted);
}
.palette-match {
  color: var(--primary-text);
  font-weight: 600;
}
.palette-preview {
  flex: 0 0 280px;
  min-width: 0;
  border-left: 1px solid var(--border);
  padding: 12px 14px;
}
.palette-preview-title {
  font-size: 14px;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.palette-preview-subtitle {
  margin-top: 2px;
  font-size: 13px;
  color: var(--muted);
  overflow-wrap: anywhere;
}
.palette-preview-row {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  margin-top: 8px;
  font-size: 13px;
  color: var(--muted);
}
.palette-preview-row span:last-child {
  text-align: right;
  overflow-wrap: anywhere;
  min-width: 0;
}
.palette-state {
  text-transform: capitalize;
}
.palette-message {
  padding: 14px;
  font-size: 14px;
  color: var(--muted);
  text-align: center;
}
@media (max-width: 600px) {
  .palette-preview {
    display: none;
  }
}
@media (prefers-reduced-motion: no-preference) {
  .command-palette {
    animation: palette-open 120ms ease;
  }
  .palette-option {
    transition: background-color 80ms ease;
  }
  @keyframes palette-open {
    from {
      opacity: 0;
      transform: translateY(-6px) scale(0.99);
    }
  }
}
</style>
