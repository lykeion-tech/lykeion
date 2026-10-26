<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Modal from '../components/Modal.vue';
import ComposerCalendar from '../components/ComposerCalendar.vue';
import Icon from '../components/Icon.vue';
import { api, ApiError } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import { uploadAttachment, useCard, useCards, useMe, useWorkspace } from '../api/queries';
import { formatSize, useAttachments } from '../lib/attachments';
import type { Card } from '../api/types';
import { matchesQuery } from '../lib/search';
import { studyNumber, type Picked, type Range } from '../lib/studies';
import { notify } from '../state/notice';
import type { Assignee } from '../state/task-composer';
import AssignPicker from '../components/AssignPicker.vue';
import MachinePicker from '../components/MachinePicker.vue';

const props = defineProps<{ assignee?: Assignee; projectId?: string }>();
const emit = defineEmits<{ close: [] }>();
const client = useQueryClient();
const cards = useCards();
const workspace = useWorkspace();
const me = useMe();
const isMac = navigator.platform.includes('Mac');
const machine = ref('');
let generation = 0;
watch(
  workspaceId,
  () => {
    generation++;
    machine.value = '';
    busy.value = false;
    error.value = '';
  },
  { flush: 'sync' },
);
onUnmounted(() => generation++);
const title = ref('');
const question = ref('');
const deadline = ref('');
const followsFrom = ref<string[]>([]);
const another = ref(false);
const expanded = ref(false);
const busy = ref(false);
const error = ref('');
const duplicate = ref<{ id: string; number: number } | null>(null);
const menu = ref('');
const query = ref('');
const form = ref<HTMLFormElement>();
const variables = ref<{ key: number; name: string; value: string }[]>([]);
let nextVariable = 0;
const files = useAttachments(uploadAttachment);
async function pick(event: Event) {
  const input = event.target as HTMLInputElement;
  const picked = [...(input.files ?? [])];
  input.value = '';
  await files.add(picked);
}
const assignee = ref<Picked | null>(props.assignee ? { ...props.assignee } : null);
function initialRange(): Range {
  const a = props.assignee;
  const columns = a?.type === 'team' ? Object.keys(a.teams) : [];
  return columns.length === 1 && a?.type === 'team' && !a.agent_id
    ? { from: columns[0]!, to: columns[0]! }
    : { from: 'grounding', to: 'reporting' };
}
const range = ref<Range>(initialRange());
const picker = ref<InstanceType<typeof AssignPicker>>();
const usedLastSet = ref(false);

const teamColumns = computed(() => picker.value?.teamColumns ?? []);
const assigneeName = computed(() => picker.value?.name ?? '');
const hint = computed(
  () => `${assigneeName.value} will start working right after you create the study`,
);

// The last set is the Teams of the creator's newest end-to-end study that had Teams.
const lastSetId = computed(
  () =>
    [...(cards.data.value ?? [])]
      .filter(
        (c) =>
          c.owner_id === me.data.value?.id &&
          c.assignee_type === ('team' as string) &&
          c.scope_from === 'grounding' &&
          c.scope_to === 'reporting',
      )
      .sort((a, b) => b.created_at.localeCompare(a.created_at))[0]?.id ?? '',
);
const lastSetStudy = useCard(lastSetId);
const lastSet = computed(() => {
  const c = lastSetStudy.data.value;
  if (!c || !lastSetId.value) return null;
  const teams = Object.fromEntries(Object.entries(c.teams).map(([col, t]) => [col, t.pack_id]));
  return Object.keys(teams).length ? { type: 'team' as const, teams } : null;
});
const number = (n: number) => studyNumber(workspace.data.value, { number: n });
const byId = (id: string) => cards.data.value?.find((c) => c.id === id);
const candidates = computed(() =>
  (cards.data.value ?? []).filter(
    (c) =>
      !followsFrom.value.includes(c.id) &&
      matchesQuery(`${number(c.number)} ${c.title}`, query.value),
  ),
);

async function openMenu(name: string) {
  menu.value = menu.value === name ? '' : name;
  query.value = '';
  await nextTick();
  form.value?.querySelector<HTMLInputElement>('.composer-menu input')?.focus();
}
function closeMenu() {
  menu.value = '';
  query.value = '';
}
function follow(id: string) {
  if (!followsFrom.value.includes(id)) followsFrom.value = [...followsFrom.value, id];
  duplicate.value = null;
  closeMenu();
}
function outside(event: PointerEvent) {
  if (!(event.target as HTMLElement).closest('.property-anchor')) closeMenu();
}
function keysDown(event: KeyboardEvent) {
  if (event.key === 'Escape' && menu.value) {
    event.preventDefault();
    event.stopPropagation();
    closeMenu();
  }
}

// A Team with no stage label has no column until the researcher picks one.
const unstaged = computed(() => picker.value?.unstaged ?? false);
const blocked = computed(
  () =>
    busy.value ||
    !question.value.trim() ||
    unstaged.value ||
    files.uploading.value ||
    files.failed.value,
);

async function submit() {
  if (blocked.value) return;
  const workspace = workspaceId.value;
  const visit = generation;
  const current = () => generation === visit && workspaceId.value === workspace;
  busy.value = true;
  error.value = '';
  duplicate.value = null;
  // No Team picked sends no assignee, and the built-in Teams work the range;
  // a single Team keeps its own stage, so the server takes no range for it,
  // while a Team set always runs its range.
  const fields = {
    ...(machine.value ? { machine: machine.value } : {}),
    ...(props.projectId ? { research_id: props.projectId } : {}),
    title: title.value.trim(),
    question: question.value.trim(),
    deadline: deadline.value ? new Date(`${deadline.value}T23:59:59`).toISOString() : null,
    follows_from: followsFrom.value,
    env: Object.fromEntries(
      variables.value
        .filter((row) => row.name !== '' || row.value !== '')
        .map((row) => [row.name, row.value]),
    ),
    ...(files.ids.value.length ? { attachment_ids: files.ids.value } : {}),
    ...(assignee.value ? { assignee: assignee.value } : {}),
    ...(assignee.value && teamColumns.value.length === 1 && !picker.value?.set
      ? {}
      : { scope: range.value }),
  };
  try {
    await api<Card>('POST', '/api/cards', fields, undefined, { workspace });
    if (!current()) return;
    if (props.projectId)
      await client.invalidateQueries({
        queryKey: keys.research(workspace, props.projectId),
      });
    await client.invalidateQueries({ queryKey: keys.cards(workspace) });
    if (!current()) return;
    notify('Study created.');
    if (another.value) {
      title.value = question.value = deadline.value = machine.value = '';
      assignee.value = props.assignee ? { ...props.assignee } : null;
      range.value = initialRange();
      followsFrom.value = [];
      variables.value = [];
      files.clear();
      await nextTick();
      form.value?.querySelector<HTMLInputElement>('.composer-title')?.focus();
    } else emit('close');
  } catch (e) {
    if (!current()) return;
    if (e instanceof ApiError && e.status === 409 && typeof e.body.card_id === 'string')
      duplicate.value = { id: e.body.card_id, number: Number(e.body.number) };
    else if (usedLastSet.value && /add this Team/.test((e as Error).message))
      error.value = "A Team in your last set isn't in your workspace any more";
    else error.value = (e as Error).message;
  } finally {
    if (current()) busy.value = false;
  }
}
onMounted(() => document.addEventListener('pointerdown', outside));
onUnmounted(() => document.removeEventListener('pointerdown', outside));
</script>
<template>
  <Modal
    title="New study"
    :class="['task-composer', 'manual', { expanded }]"
    @close="emit('close')"
  >
    <!-- eslint-disable vue/use-v-on-exact -->
    <form
      ref="form"
      @submit.prevent="submit"
      @keydown="keysDown"
      @keydown.meta.enter.prevent="submit"
      @keydown.ctrl.enter.prevent="submit"
    >
      <!-- eslint-enable vue/use-v-on-exact -->
      <header class="composer-breadcrumb">
        <span>{{ workspace.data.value?.name }}</span>
        <Icon name="right" :size="12" />
        <strong>New study</strong>
        <button
          type="button"
          class="icon-button push-right"
          :aria-label="expanded ? 'Collapse' : 'Expand'"
          :aria-pressed="expanded"
          @click="expanded = !expanded"
        >
          <Icon name="expand" :size="15" />
        </button>
        <button type="button" class="icon-button" aria-label="Close" @click="emit('close')">
          <Icon name="close" :size="15" />
        </button>
      </header>
      <div class="composer-body">
        <input
          v-model="title"
          autofocus
          class="composer-title"
          placeholder="Study title"
          aria-label="Study title"
        />
        <textarea
          v-model="question"
          class="composer-description"
          placeholder="Describe what you want to do, in your own words"
          aria-label="Describe your study"
          required
        />
      </div>
      <p class="composer-hint">{{ hint }}</p>
      <p v-if="duplicate" class="composer-warning" role="status">
        An active study already asks this: {{ number(duplicate.number) }}
        <template v-if="byId(duplicate.id)">“{{ byId(duplicate.id)!.title }}”</template>. Set it as
        “Follows from” to create a follow-up.
        <button type="button" class="text-button" @click="follow(duplicate.id)">
          Follows from {{ number(duplicate.number) }}
        </button>
      </p>
      <div class="composer-properties">
        <div class="property-line">
          <MachinePicker v-model="machine" :disabled="busy" />
          <div class="property-anchor">
            <button type="button" class="property-pill" @click="openMenu('deadline')">
              <Icon name="clock" :size="14" />{{ deadline || 'No deadline' }}</button
            ><ComposerCalendar
              v-if="menu === 'deadline'"
              v-model="deadline"
              kind="due"
              @close="closeMenu"
            />
          </div>
          <AssignPicker
            ref="picker"
            v-model="assignee"
            v-model:range="range"
            :last-set="lastSet"
            @open="closeMenu"
            @picked="(last) => (usedLastSet = last)"
          />
          <div v-for="id in followsFrom" :key="id" class="property-anchor">
            <button
              type="button"
              class="property-pill"
              :aria-label="`Follows from: ${byId(id) ? number(byId(id)!.number) : id}`"
              @click="followsFrom = followsFrom.filter((x) => x !== id)"
            >
              <Icon name="up" :size="12" />{{ byId(id) ? number(byId(id)!.number) : 'Study'
              }}<Icon name="close" :size="12" />
            </button>
          </div>
          <div class="property-anchor">
            <button type="button" class="property-pill" @click="openMenu('follows')">
              <Icon name="link" :size="14" />Follows from
            </button>
            <div v-if="menu === 'follows'" class="composer-menu" role="menu">
              <input v-model="query" placeholder="Search studies…" aria-label="Search studies" />
              <button
                v-for="c in candidates.slice(0, 8)"
                :key="c.id"
                type="button"
                @click="follow(c.id)"
              >
                <small>{{ number(c.number) }}</small
                ><span>{{ c.title }}</span>
              </button>
              <p v-if="!candidates.length">No matching studies</p>
            </div>
          </div>
        </div>
      </div>
      <div class="composer-files">
        <label class="text-button" for="composer-files-input">
          <Icon name="link" :size="14" />Attach files
        </label>
        <input
          id="composer-files-input"
          type="file"
          multiple
          class="visually-hidden"
          @change="pick"
        />
        <small
          >Up to 100 files, 5 GB each, 10 GB in all; archives are unpacked for the agents</small
        >
        <ul v-if="files.items.value.length" aria-label="Files to attach">
          <li v-for="f in files.items.value" :key="f.key" tabindex="0">
            <span class="file-name">{{ f.name }}</span>
            <small>{{ formatSize(f.size) }}</small>
            <small v-if="f.error" class="composer-error" role="alert">{{ f.error }}</small>
            <progress
              v-else-if="!f.id"
              max="1"
              :value="f.progress"
              :aria-label="`Uploading ${f.name}`"
            />
            <button
              type="button"
              class="text-button"
              :aria-label="`Remove ${f.name}`"
              @click="files.remove(f.key)"
            >
              Remove
            </button>
          </li>
        </ul>
        <p v-if="files.error.value" class="composer-error" role="alert">{{ files.error.value }}</p>
      </div>
      <section class="composer-variables" aria-label="Study variables">
        <div class="variables-header">
          <span>Study variables</span>
          <button
            type="button"
            class="text-button"
            @click="variables.push({ key: nextVariable++, name: '', value: '' })"
          >
            Add variable
          </button>
        </div>
        <p>Visible to everyone in this study. Not for passwords or keys; use a connector.</p>
        <div class="variable-rows">
          <div v-for="(row, index) in variables" :key="row.key" class="variable-row">
            <input
              v-model="row.name"
              placeholder="Name"
              :aria-label="`Variable name ${index + 1}`"
            />
            <input
              v-model="row.value"
              placeholder="Value"
              :aria-label="`Variable value ${index + 1}`"
            />
            <button
              type="button"
              class="text-button"
              :aria-label="`Remove variable ${index + 1}`"
              @click="variables = variables.filter((v) => v.key !== row.key)"
            >
              Remove
            </button>
          </div>
        </div>
      </section>
      <p v-if="error" class="composer-error" role="alert">{{ error }}</p>
      <footer class="composer-footer">
        <label class="another-switch push-right">
          <input v-model="another" type="checkbox" role="switch" aria-label="Create another" />
          <span class="switch-track" />Create another</label
        >
        <button type="submit" class="button primary compact" :disabled="blocked">
          {{ busy ? 'Saving…' : 'Create study' }}<kbd>{{ isMac ? '⌘' : 'Ctrl' }}</kbd> <kbd>↵</kbd>
        </button>
      </footer>
    </form>
  </Modal>
</template>
<style scoped src="../components/composer-menu.css"></style>
<style scoped>
.task-composer {
  width: 578px;
  max-width: calc(100vw - 48px);
  background: var(--modal);
  overflow: visible;
}
.task-composer.manual {
  width: 676px;
}
.task-composer.expanded {
  width: min(1100px, calc(100vw - 64px));
}
.task-composer form {
  position: relative;
  display: flex;
  flex-direction: column;
  min-height: 308px;
}
.task-composer.manual form {
  min-height: 384px;
}
.task-composer.expanded form {
  height: min(740px, 85vh);
}
.composer-breadcrumb {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 20px;
  height: 50px;
  flex: none;
  font-size: 12px;
}
.composer-breadcrumb > span {
  color: var(--muted);
}
.composer-breadcrumb strong {
  font-weight: 400;
}
.composer-breadcrumb > .icon {
  color: var(--icon-dim);
}
.composer-breadcrumb .icon-button {
  width: 24px;
  height: 24px;
}
.composer-body {
  display: flex;
  flex-direction: column;
  padding: 0 20px;
  flex: 1;
  min-height: 166px;
}
.manual .composer-body {
  min-height: 171px;
}
.composer-title {
  padding: 0;
  border: 0;
  background: none;
  font-size: 18px;
  font-weight: 500;
  line-height: 25px;
  outline: 0;
  width: 100%;
  margin-bottom: 10px;
}
.composer-description,
.composer-prompt {
  padding: 0;
  border: 0;
  background: none;
  font-size: 14px;
  line-height: 23px;
  resize: none;
  outline: 0;
  flex: 1;
  min-height: 120px;
  width: 100%;
}
.composer-prompt {
  margin-top: 7px;
}
.composer-title::placeholder,
.composer-description::placeholder,
.composer-prompt::placeholder {
  color: var(--muted);
  opacity: 1;
}
.created-by {
  display: flex;
  align-items: center;
  gap: 8px;
  position: relative;
  font-size: 12px;
  height: 22px;
}
.created-by > span {
  color: var(--muted);
}
.created-by > button {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 0;
  font-size: 12px;
  max-width: calc(100% - 70px);
}
.created-by > button > .icon:last-child {
  opacity: 0;
}
.created-by > button:hover > .icon:last-child {
  opacity: 1;
}
.composer-properties {
  position: relative;
  padding: 8px 16px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.property-line {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  min-height: 26px;
}
.more-pill {
  padding: 0 10px;
}
.assignee-note {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 8px 20px;
  margin: 0;
  color: var(--muted);
  font-size: 11px;
  line-height: 16px;
}
.composer-footer {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 16px;
  border-top: 1px solid #ffffff40;
  min-height: 54px;
  flex: none;
}
.composer-footer > .button {
  padding: 0 9px;
  gap: 5px;
  height: 26px;
  font-weight: 400;
  line-height: 18px;
}
.composer-footer kbd {
  height: 20px;
  min-width: 23px;
  border: 1px solid #ffffff4d;
  color: white;
  background: #ffffff15;
  font-size: 13px;
  padding: 0 3px;
}
.attachment-button {
  width: 24px;
  height: 24px;
}
.mode-switch {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--muted);
  padding: 4px 6px;
  white-space: nowrap;
  border-radius: 5px;
}
.mode-switch:hover {
  background: #cdd9e433;
}
.another-switch {
  display: flex;
  align-items: center;
  gap: 7px;
  white-space: nowrap;
  position: relative;
  font-size: 12px;
  color: var(--muted);
  cursor: pointer;
}
.another-switch input {
  position: absolute;
  opacity: 0;
  width: 24px;
  height: 16px;
  margin: 0;
}
.switch-track {
  height: 14px;
  width: 24px;
  border-radius: 12px;
  background: var(--switch-track);
  padding: 1px;
  display: flex;
}
.switch-track:before {
  content: '';
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: #fff;
}
.another-switch input:checked + .switch-track {
  background: var(--primary);
  justify-content: flex-end;
}
.another-switch input:focus-visible + .switch-track {
  outline: 2px solid var(--primary);
  outline-offset: 3px;
}
.composer-error {
  color: var(--error-text-composer);
  font-size: 12px;
  line-height: 18px;
  padding: 4px 20px 12px;
}
.more-menu {
  min-width: 164px;
  max-width: 190px;
  background: var(--fill-menu);
  padding: 4px;
}
.more-menu > button {
  font-size: 12px;
  min-height: 28px;
  padding: 5px 7px;
  gap: 7px;
}
.more-menu hr {
  border: 0;
  border-top: 1px solid #ffffff60;
  margin: 3px -4px;
}
.task-relation-backdrop {
  position: absolute;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding-top: 32px;
}
.task-relation-picker {
  width: 386px;
  max-width: calc(100vw - 40px);
  background: var(--fill-menu);
  border: 1px solid #ffffff70;
  border-radius: 14px;
  padding: 8px;
  box-shadow: 0 12px 36px #0002;
}
.task-relation-picker header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.task-relation-picker h2 {
  font-size: 16px;
  margin: 0;
}
.task-relation-picker > p {
  font-size: 12px;
  color: var(--muted);
  margin: 8px 0 15px;
}
.relation-search {
  display: flex;
  align-items: center;
  gap: 7px;
  background: #ffffff12;
  border: 1px solid #ffffff30;
  border-radius: 7px;
  padding: 7px 9px;
}
.relation-search input {
  background: none;
  border: 0;
  outline: 0;
  font-size: 14px;
  width: 100%;
}
.relation-results [role='listbox'] > button {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 9px 8px;
  font-size: 14px;
  border-radius: 5px;
  text-align: left;
}
.relation-results [role='listbox'] > button:hover,
.relation-results [role='listbox'] > button:focus-visible {
  background: #ffffff50;
}
.relation-results {
  position: static;
  padding: 0;
  min-width: 100%;
  max-width: none;
  background: transparent;
  border: 0;
  box-shadow: none;
}
.relation-results small {
  margin: 0;
  white-space: nowrap;
}
.relation-results span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.properties-menu-anchor {
  position: absolute;
  bottom: 8px;
  left: 16px;
}
.expanded .composer-body {
  min-height: 0;
}
.expanded .composer-description,
.expanded .composer-prompt {
  min-height: 0;
}
@media (max-width: 650px) {
  .composer-footer {
    gap: 5px;
    padding: 12px;
  }
  .composer-footer kbd {
    display: none;
  }
  .mode-switch {
    font-size: 11px;
    padding: 4px;
  }
  .another-switch {
    font-size: 11px;
  }
}
.composer-files,
.composer-variables {
  padding: 0 20px 8px;
  font-size: 12px;
}
.variables-header,
.variable-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.variables-header {
  justify-content: space-between;
}
.composer-variables > p {
  margin: 4px 0 8px;
  color: var(--muted);
}
.variable-row {
  margin-top: 6px;
}
.variable-rows {
  max-height: 120px;
  overflow-y: auto;
}
.variable-row input {
  width: 0;
  min-width: 0;
  flex: 1;
  font: inherit;
}
.variable-row input:first-child {
  flex: 0.7;
}
.composer-files > small {
  margin-left: 8px;
  color: var(--muted);
}
.composer-files label {
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  gap: 5px;
}
.composer-files ul {
  list-style: none;
  margin: 6px 0 0;
  padding: 0;
  max-height: 120px;
  overflow: auto;
}
.composer-files li {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 24px;
}
.composer-files li:focus-visible {
  outline: 2px solid var(--primary);
}
.composer-files .file-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
}
.composer-files .composer-error {
  padding: 0;
}
.composer-files:has(input:focus-visible) label {
  outline: 2px solid var(--primary);
}
.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
}
.composer-hint {
  margin: 0;
  padding: 0 20px 8px;
  color: var(--muted);
  font-size: 12px;
}
.composer-warning {
  margin: 0 20px 6px;
  font-size: 12.5px;
  color: #8a6a1c;
  background: #fbf3df;
  border-radius: 6px;
  padding: 6px 10px;
}
.composer-warning .text-button {
  font-size: 12.5px;
  text-decoration: underline;
}
</style>
