<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import type { Runtime } from '../../../../packages/contracts/src/research';
import Icon from './Icon.vue';
import { Brain, Cpu } from '@lucide/vue';
interface ModelOption {
  id: string;
  name: string;
  provider?: string;
  runtimeId?: string;
}
const props = withDefaults(
  defineProps<{
    runtimeId: string;
    model: string;
    thinking: string;
    runtimes: Runtime[];
    models?: ModelOption[];
    loadingModels?: boolean;
  }>(),
  { models: () => [], loadingModels: false },
);
const emit = defineEmits<{
  'update:runtimeId': [string];
  'update:model': [string];
  'update:thinking': [string];
  refreshModels: [string];
}>();
const opened = ref<'runtime' | 'model' | 'thinking' | null>(null),
  query = ref(''),
  root = ref<HTMLElement>(),
  popup = ref<HTMLElement>(),
  popupStyle = ref<Record<string, string>>({});
let trigger: HTMLElement | null = null;
const levels = [
  { id: 'low', name: 'Low' },
  { id: 'medium', name: 'Medium' },
  { id: 'high', name: 'High' },
  { id: 'xhigh', name: 'Extra high' },
  { id: 'max', name: 'Max' },
];
const runtime = computed(() => props.runtimes.find((x) => x.id === props.runtimeId));
const availableModels = computed(() =>
  props.models.filter((x) => !x.runtimeId || x.runtimeId === props.runtimeId),
);
const filteredModels = computed(() =>
  availableModels.value.filter((x) =>
    (x.name + ' ' + x.id + ' ' + (x.provider ?? ''))
      .toLowerCase()
      .includes(query.value.toLowerCase()),
  ),
);
const filteredRuntimes = computed(() =>
  props.runtimes.filter((x) =>
    (x.name + ' ' + x.platform).toLowerCase().includes(query.value.toLowerCase()),
  ),
);
const modelLabel = computed(
  () =>
    availableModels.value.find((x) => x.id === props.model)?.name ||
    props.model ||
    'Default (provider)',
);
function close() {
  opened.value = null;
  trigger?.focus();
}
async function open(kind: 'runtime' | 'model' | 'thinking', event: MouseEvent) {
  if (opened.value === kind) {
    close();
    return;
  }
  trigger = event.currentTarget as HTMLElement;
  query.value = '';
  opened.value = kind;
  await nextTick();
  position();
  await nextTick();
  (popup.value?.querySelector('input,button') as HTMLElement)?.focus();
}
function position() {
  if (!opened.value || !trigger) return;
  const box = trigger.getBoundingClientRect(),
    width = Math.min(
      window.innerWidth - 24,
      Math.max(opened.value === 'thinking' ? 288 : 320, box.width),
    ),
    below = window.innerHeight - box.bottom - 16,
    above = box.top - 16,
    height = Math.min(opened.value === 'thinking' ? 208 : 348, Math.max(below, above));
  popupStyle.value = {
    width: width + 'px',
    left: Math.max(12, Math.min(window.innerWidth - width - 12, box.right - width)) + 'px',
    maxHeight: height + 'px',
    ...(below >= height
      ? { top: box.bottom + 4 + 'px' }
      : { bottom: window.innerHeight - box.top + 4 + 'px' }),
  };
}
function choose(kind: 'runtime' | 'model' | 'thinking', value: string) {
  if (kind === 'runtime') emit('update:runtimeId', value);
  else if (kind === 'model') emit('update:model', value);
  else emit('update:thinking', value);
  close();
}
function key(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    close();
    return;
  }
  if (
    event.key === 'Enter' &&
    event.target instanceof HTMLInputElement &&
    opened.value === 'model' &&
    query.value.trim()
  ) {
    event.preventDefault();
    choose('model', query.value.trim());
    return;
  }
  if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
    if (event.target instanceof HTMLInputElement && ['Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    const options = [...popup.value!.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')],
      current = options.indexOf(document.activeElement as HTMLButtonElement),
      index =
        event.key === 'Home'
          ? 0
          : event.key === 'End'
            ? options.length - 1
            : (current + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length;
    options[index]?.focus();
  }
  if (event.key === 'Tab') opened.value = null;
}
function outside(event: PointerEvent) {
  if (!root.value?.contains(event.target as Node) && !popup.value?.contains(event.target as Node))
    opened.value = null;
}
onMounted(() => {
  document.addEventListener('pointerdown', outside);
  window.addEventListener('resize', position);
  window.addEventListener('scroll', position, true);
});
onUnmounted(() => {
  document.removeEventListener('pointerdown', outside);
  window.removeEventListener('resize', position);
  window.removeEventListener('scroll', position, true);
});
</script>
<template>
  <div ref="root" class="execution-options">
    <div class="execution-top">
      <div class="execution-field">
        <span>Runtime</span
        ><button
          type="button"
          class="execution-trigger runtime-trigger"
          aria-label="Runtime"
          aria-haspopup="listbox"
          :aria-expanded="opened === 'runtime'"
          @click="open('runtime', $event)"
        >
          <Icon name="monitor" :size="17" /><span
            >{{ runtime?.name || runtimeId || 'Choose a runtime'
            }}<small v-if="runtime">{{ runtime.platform }} · {{ runtime.status }}</small></span
          ><Icon class="push-right" name="down" :size="14" />
        </button>
      </div>
      <div class="execution-field">
        <span>Model</span
        ><button
          type="button"
          class="execution-trigger"
          aria-label="Model"
          aria-haspopup="listbox"
          :aria-expanded="opened === 'model'"
          @click="open('model', $event)"
        >
          <Cpu :size="16" :stroke-width="1.7" aria-hidden="true" /><span>{{ modelLabel }}</span
          ><Icon class="push-right" name="down" :size="14" />
        </button>
      </div>
    </div>
    <div class="thinking-row">
      <span>Thinking</span
      ><button
        type="button"
        class="execution-trigger"
        :aria-label="
          'Thinking · ' +
          (levels.find((x) => x.id === thinking)?.name || thinking || 'Follow CLI config')
        "
        aria-haspopup="listbox"
        :aria-expanded="opened === 'thinking'"
        @click="open('thinking', $event)"
      >
        <Brain :size="16" :stroke-width="1.7" aria-hidden="true" /><span>{{
          levels.find((x) => x.id === thinking)?.name || thinking || 'Follow CLI config'
        }}</span
        ><Icon class="push-right" name="down" :size="14" />
      </button>
    </div>
  </div>
  <Teleport to="body"
    ><div v-if="opened" ref="popup" class="execution-popup" :style="popupStyle" @keydown="key">
      <div v-if="opened !== 'thinking'" class="execution-search">
        <input
          v-model="query"
          :aria-label="opened === 'model' ? 'Search or type a model ID' : 'Search runtimes'"
          :placeholder="opened === 'model' ? 'Search or type a model ID' : 'Search runtimes'"
        /><button
          v-if="opened === 'model'"
          type="button"
          class="icon-button"
          aria-label="Refresh models"
          :disabled="loadingModels || !runtimeId"
          @click="emit('refreshModels', runtimeId)"
        >
          <Icon name="refresh" :size="15" />
        </button>
      </div>
      <div
        class="execution-list"
        role="listbox"
        :aria-label="
          opened === 'model' ? 'Models' : opened === 'runtime' ? 'Runtimes' : 'Thinking levels'
        "
      >
        <template v-if="opened === 'runtime'"
          ><button
            type="button"
            role="option"
            :aria-selected="!runtimeId"
            @click="choose('runtime', '')"
          >
            Use runtime default</button
          ><button
            v-for="item in filteredRuntimes"
            :key="item.id"
            type="button"
            role="option"
            :aria-selected="runtimeId === item.id"
            @click="choose('runtime', item.id)"
          >
            <Icon name="monitor" :size="16" /><span
              >{{ item.name }}<small>{{ item.platform }} · {{ item.status }}</small></span
            ><Icon v-if="runtimeId === item.id" class="push-right" name="check" :size="14" />
          </button>
          <p v-if="!filteredRuntimes.length">
            {{ runtimes.length ? 'No matching runtimes.' : 'No runtimes connected.' }}
          </p></template
        >
        <template v-else-if="opened === 'model'"
          ><button
            v-if="!query"
            type="button"
            role="option"
            :aria-selected="!model"
            @click="choose('model', '')"
          >
            Default (provider)<Icon
              v-if="!model"
              class="push-right"
              name="check"
              :size="14"
            /></button
          ><button
            v-for="item in filteredModels"
            :key="item.id"
            type="button"
            role="option"
            :aria-selected="model === item.id"
            @click="choose('model', item.id)"
          >
            <span
              ><small v-if="item.provider" class="provider">{{ item.provider }}</small
              >{{ item.name }}<small>{{ item.id }}</small></span
            >
          </button>
          <p v-if="loadingModels">Loading models…</p>
          <p v-else-if="!filteredModels.length">
            {{
              availableModels.length
                ? 'No matching models.'
                : 'This runtime has not supplied a model catalog.'
            }}
          </p>
          <button
            v-if="query.trim() && !availableModels.some((x) => x.id === query.trim())"
            type="button"
            role="option"
            :aria-selected="model === query.trim()"
            @click="choose('model', query.trim())"
          >
            <Icon name="plus" :size="14" /><span
              >Use {{ query.trim() }}<small>Custom ID · availability is not verified</small></span
            >
          </button></template
        >
        <template v-else
          ><button
            v-for="level in levels"
            :key="level.id"
            type="button"
            role="option"
            :aria-selected="thinking === level.id"
            @click="choose('thinking', level.id)"
          >
            {{ level.name
            }}<Icon
              v-if="thinking === level.id"
              class="push-right"
              name="check"
              :size="14"
            /></button
          ><button
            v-if="thinking"
            class="reset-choice"
            type="button"
            role="option"
            :aria-selected="false"
            @click="choose('thinking', '')"
          >
            Follow CLI config
          </button></template
        >
      </div>
    </div></Teleport
  >
</template>
<style scoped>
.execution-options {
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 12px;
  overflow: hidden;
}
.execution-top {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
  padding: 20px 16px 16px;
}
.execution-field {
  min-width: 0;
}
.execution-field > span {
  display: block;
  font-size: 12px;
  color: var(--muted);
  margin-bottom: 10px;
}
.execution-trigger {
  display: flex;
  align-items: center;
  gap: 10px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 10px 12px;
  min-height: 42px;
  width: 100%;
  text-align: left;
  font-size: 14px;
}
.execution-trigger > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.execution-trigger small {
  display: block;
  font-size: 12px;
  line-height: 18px;
  color: var(--muted);
}
.execution-trigger[aria-expanded='true'] {
  background: var(--selected);
}
.runtime-trigger {
  min-height: 58px;
}
.thinking-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-top: 1px solid var(--border);
  padding: 14px 16px;
  gap: 16px;
}
.thinking-row > .execution-trigger {
  width: 288px;
  background: transparent;
  border-color: transparent;
  min-height: 40px;
}
.execution-popup {
  position: fixed;
  z-index: 95;
  background: var(--modal);
  border: 1px solid var(--border);
  border-radius: 9px;
  box-shadow: 0 6px 18px #0002;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: 4px;
  font-size: 14px;
}
.execution-search {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px;
  border-bottom: 1px solid var(--border);
  flex: none;
}
.execution-search input {
  min-width: 0;
  flex: 1;
  width: 100%;
  border: 1px solid color-mix(in srgb, var(--surface) 30%, transparent);
  border-radius: 6px;
  padding: 6px 10px;
  background: transparent;
  outline: none;
}
.execution-list {
  overflow: auto;
  min-height: 0;
}
.execution-list > button {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  text-align: left;
  padding: 7px 8px;
  min-height: 32px;
  border-radius: 5px;
  font-size: 14px;
}
.execution-list > button:hover,
.execution-list > button:focus-visible {
  background: var(--hover);
}
.execution-list small {
  display: block;
  color: var(--muted);
  font-size: 12px;
  margin-top: 3px;
  overflow-wrap: anywhere;
}
.execution-list small.provider {
  text-transform: uppercase;
  margin-bottom: 6px;
}
.execution-list p {
  padding: 14px 10px;
  color: var(--muted);
  font-size: 12px;
  line-height: 18px;
}
.reset-choice {
  border-top: 1px solid var(--border) !important;
  margin-top: 3px;
}
.execution-list > button > span {
  min-width: 0;
  overflow-wrap: anywhere;
}
@media (max-width: 650px) {
  .execution-top {
    grid-template-columns: 1fr;
  }
  .thinking-row > .execution-trigger {
    width: 65%;
  }
}
</style>
