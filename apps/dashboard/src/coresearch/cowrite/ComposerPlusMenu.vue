<script setup lang="ts">
import { parseMarkdownBlocks } from '../../lib/markdown';
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useSkills, useConnectors } from '../../api/queries';
import { useAvailabilityChanges } from '../../state/availability';
import { useTemplates } from './api';
const props = defineProps<{ cowrite?: boolean; template: string }>();
const emit = defineEmits<{
  close: [];
  attach: [];
  'update:template': [string];
  manage: [string];
}>();
const skills = useSkills(),
  connectors = useConnectors(),
  changes = useAvailabilityChanges();
const templates = useTemplates(() => (props.cowrite ? 'new' : ''));
const sub = ref(''),
  root = ref<HTMLElement>(),
  room = ref<number>();
const sections = computed(() => [
  {
    id: 'skills',
    label: 'Skills',
    paths: ['M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8z'],
  },
  {
    id: 'connectors',
    label: 'Connectors',
    paths: ['M9 2v6M15 2v6M7 8h10v3a5 5 0 0 1-10 0z', 'M12 16v6'],
  },
  ...(props.cowrite && templates.data.value?.length
    ? [
        {
          id: 'template',
          label: 'Template',
          paths: ['M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z', 'M14 3v5h5'],
        },
      ]
    : []),
]);
function measure() {
  const menu = root.value;
  if (!sub.value || !menu?.getBoundingClientRect) return;
  let top = 0;
  for (let node = menu.parentElement; node; node = node.parentElement) {
    if (['auto', 'scroll', 'hidden'].includes(getComputedStyle(node).overflowY)) {
      top = node.getBoundingClientRect().top;
      break;
    }
  }
  room.value = Math.max(0, Math.round(menu.getBoundingClientRect().bottom - top - 8));
}
watch(sub, async () => {
  await nextTick();
  measure();
});
onMounted(() => window.addEventListener('resize', measure));
onUnmounted(() => window.removeEventListener('resize', measure));
function manage(page: string) {
  emit('close');
  emit('manage', page);
}
const inline = (text?: string) =>
  parseMarkdownBlocks(text ?? '')
    .filter((t) => t.type === 'inline')
    .map((t) => t.html)
    .join('')
    .replace(/<\/?a\b[^>]*>/g, '');
</script>
<template>
  <div
    ref="root"
    class="composer-popover composer-plus-menu"
    role="menu"
    aria-label="Add"
    :style="room === undefined ? undefined : { '--cmenu-room': `${room}px` }"
  >
    <button
      type="button"
      role="menuitem"
      class="cmenu-row"
      @click="
        emit('attach');
        emit('close');
      "
    >
      <span class="cmenu-icon"
        ><svg
          width="15"
          height="15"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.75"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path
            d="M21 11.5 12.5 20a5 5 0 0 1-7-7l8-8a3.3 3.3 0 0 1 4.7 4.7l-8 8a1.6 1.6 0 0 1-2.3-2.3l7.4-7.4"
          /></svg
      ></span>
      <span class="cmenu-label">Add files or photos</span>
    </button>
    <div v-for="section in sections" :key="section.id" class="cmenu-sub-host">
      <button
        type="button"
        role="menuitem"
        aria-haspopup="menu"
        :aria-expanded="sub === section.id"
        class="cmenu-row"
        :class="{ 'is-open': sub === section.id }"
        @click="sub = sub === section.id ? '' : section.id"
      >
        <span class="cmenu-icon"
          ><svg
            width="15"
            height="15"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.75"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path v-for="path in section.paths" :key="path" :d="path" /></svg
        ></span>
        <span class="cmenu-label">{{ section.label }}</span
        ><span class="cmenu-chevron"
          ><svg
            width="12"
            height="12"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.75"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="m9 6 6 6-6 6" /></svg
        ></span>
      </button>
      <div
        v-if="sub === section.id"
        class="composer-popover cmenu-submenu"
        role="menu"
        :aria-label="section.label"
      >
        <div class="cmenu-scroll">
          <template v-if="section.id === 'template'">
            <button
              v-for="item in templates.data.value ?? []"
              :key="item.id"
              type="button"
              role="menuitemradio"
              :aria-checked="item.id === template"
              class="cmenu-row"
              @click="
                emit('update:template', item.id);
                emit('close');
              "
            >
              <span class="cmenu-label">{{ item.label }}</span
              ><svg
                v-if="item.id === template"
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="1.75"
                stroke-linecap="round"
                stroke-linejoin="round"
                class="csend-item-check"
                aria-hidden="true"
              >
                <path d="m5 12 5 5L20 6" />
              </svg>
            </button>
          </template>
          <template v-else>
            <div
              v-for="row in section.id === 'skills'
                ? (skills.data.value ?? []).map((s) => ({
                    id: s.id,
                    name: s.name,
                    description: s.description,
                    enabled: s.policy_enabled,
                    revision: s.policy_revision,
                  }))
                : (connectors.data.value ?? []).map((c) => ({
                    id: c.name,
                    name: c.name,
                    description: c.purpose,
                    enabled: c.policy_enabled,
                    revision: c.revision,
                  }))"
              :key="row.id"
              class="cmenu-row cmenu-row--switch"
              :title="section.id === 'skills' ? row.description : undefined"
            >
              <span class="cmenu-label"
                >{{ row.name }}<span class="md-inline cmenu-desc" v-html="inline(row.description)"
              /></span>
              <button
                type="button"
                role="switch"
                :aria-checked="row.enabled"
                :aria-label="row.name"
                :disabled="
                  !!changes.pending.value ||
                  !(section.id === 'skills'
                    ? changes.allowed.value
                    : changes.catalogueAllowed.value)
                "
                class="relative h-5 w-9 shrink-0 rounded-full transition-colors disabled:cursor-not-allowed disabled:opacity-50"
                :class="row.enabled ? 'bg-accent' : 'border border-line bg-surface-2'"
                @click="
                  changes.toggle(
                    section.id === 'skills' ? 'skill' : 'catalogue',
                    row.id,
                    row.revision,
                    !row.enabled,
                  )
                "
              >
                <span
                  class="absolute top-0.5 h-4 w-4 rounded-full bg-white transition-all"
                  :class="row.enabled ? 'left-[18px]' : 'left-0.5'"
                />
              </button>
            </div>
          </template>
        </div>
        <template v-if="section.id !== 'template'"
          ><div class="cmenu-sep" role="separator" />
          <button type="button" role="menuitem" class="cmenu-row" @click="manage(section.id)">
            <span class="cmenu-label">Manage {{ section.id }}</span>
          </button></template
        >
      </div>
    </div>
    <p
      v-if="
        changes.error.value || skills.error.value || connectors.error.value || templates.error.value
      "
      role="alert"
    >
      {{
        changes.error.value ||
        skills.error.value?.message ||
        connectors.error.value?.message ||
        templates.error.value?.message
      }}
    </p>
  </div>
</template>
