<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Modal from '../../ui/Modal.vue';
import ComposerCalendar from './ComposerCalendar.vue';
import Icon from '../../ui/Icon.vue';
import { api, ApiError } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { uploadAttachment, useCard, useCards, useMe, useWorkspace } from '../../api/queries';
import { formatSize, useAttachments } from '../../lib/attachments';
import type { Card } from '../../api/types';
import {
  coversExperimentation,
  predicateKeys,
  studyNumber,
  type Picked,
  type Range,
} from '../../lib/studies';
import { notify } from '../../state/notice';
import type { Assignee } from '../../state/study-composer';
import AssignPicker from '../../ui/AssignPicker.vue';
import MachinePicker from '../machines/MachinePicker.vue';

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
const another = ref(false);
const expanded = ref(false);
const busy = ref(false);
const error = ref('');
const duplicate = ref<{ id: string; number: number } | null>(null);
const menu = ref('');
const form = ref<HTMLFormElement>();
const results = ref<{ key: number; name: string; text: string }[]>([]);
let nextResult = 0;
const addResult = () => results.value.push({ key: nextResult++, name: '', text: '' });
addResult();
// The server's refusal of one pre-registered result, shown on its row.
const resultError = ref<{ key: number; message: string } | null>(null);
const files = useAttachments(uploadAttachment);
const filesHint = 'Up to 100 files, 5 GB each, 10 GB in all; archives are unpacked for the agents';
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
    : { from: 'grounding', to: 'validation' };
}
const range = ref<Range>(initialRange());
const picker = ref<InstanceType<typeof AssignPicker>>();
const usedLastSet = ref(false);

const teamColumns = computed(() => picker.value?.teamColumns ?? []);
// A single Team with no set sends no range and works its own column.
const sendsRange = computed(
  () => !(assignee.value && teamColumns.value.length === 1 && !picker.value?.set),
);
const preregisters = computed(() =>
  coversExperimentation(sendsRange.value ? range.value : null, teamColumns.value),
);
const sentResults = computed(() =>
  results.value
    .map((row) => ({ key: row.key, name: row.name.trim(), text: row.text.trim() }))
    .filter((row) => row.name || row.text),
);
const resultKeys = computed(() => predicateKeys(sentResults.value.map((row) => row.text)));
const completeResults = computed(() => sentResults.value.filter((row) => row.name && row.text));

// Chips that don't fit the row fold into a "⋯" chip: the due date, then the machine.
const chipRow = ref<HTMLElement>();
const folded = ref(0);
let fitting = 0;
async function fit() {
  const line = chipRow.value;
  // Refolding would remount an open chip's menu; the row refits once it closes.
  if (!line || menu.value) return;
  const visit = ++fitting;
  folded.value = 0;
  await nextTick();
  const overflows = () =>
    (line.lastElementChild?.getBoundingClientRect().right ?? 0) >
    line.getBoundingClientRect().right;
  while (visit === fitting && folded.value < 2 && overflows()) {
    folded.value++;
    await nextTick();
  }
}
let resizes: ResizeObserver | undefined;
onMounted(() => {
  resizes = new ResizeObserver(() => void fit());
  if (chipRow.value) resizes.observe(chipRow.value);
  // Chips change width once the fonts load, while the row keeps its own.
  void document.fonts?.ready.then(() => fit());
});
onUnmounted(() => resizes?.disconnect());
watch(
  () => [
    picker.value?.name,
    range.value,
    preregisters.value,
    completeResults.value.length,
    machine.value,
    deadline.value,
    menu.value,
  ],
  () => void fit(),
  { flush: 'post' },
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
          c.scope_to === 'validation',
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

async function openMenu(name: string) {
  menu.value = menu.value === name ? '' : name;
  await nextTick();
  form.value?.querySelector<HTMLInputElement>('.composer-menu input')?.focus();
}
function closeMenu() {
  menu.value = '';
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
    (preregisters.value && !completeResults.value.length) ||
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
  resultError.value = null;
  const sent = preregisters.value ? sentResults.value : [];
  // No Team picked sends no assignee, and the built-in Teams work the range;
  // a single Team keeps its own stage, so the server takes no range for it,
  // while a Team set always runs its range.
  const fields = {
    ...(machine.value ? { machine: machine.value } : {}),
    ...(props.projectId ? { research_id: props.projectId } : {}),
    title: title.value.trim(),
    question: question.value.trim(),
    deadline: deadline.value ? new Date(`${deadline.value}T23:59:59`).toISOString() : null,
    ...(files.ids.value.length ? { attachment_ids: files.ids.value } : {}),
    ...(assignee.value ? { assignee: assignee.value } : {}),
    ...(sendsRange.value ? { scope: range.value } : {}),
    ...(sent.length ? { predicates: sent.map(({ name, text }) => ({ name, text })) } : {}),
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
      results.value = [];
      addResult();
      files.clear();
      await nextTick();
      form.value?.querySelector<HTMLInputElement>('.composer-title')?.focus();
    } else emit('close');
  } catch (e) {
    if (!current()) return;
    const row = resultRow(sent, (e as Error).message);
    if (e instanceof ApiError && e.status === 409 && typeof e.body.card_id === 'string')
      duplicate.value = { id: e.body.card_id, number: Number(e.body.number) };
    else if (row !== undefined) {
      resultError.value = { key: row, message: (e as Error).message };
      menu.value = 'results';
    } else if (usedLastSet.value && /add this Team/.test((e as Error).message))
      error.value = "A Team in your last set isn't in your workspace any more";
    else error.value = (e as Error).message;
  } finally {
    if (current()) busy.value = false;
  }
}
// The server names a refused result by its name, or by its position when it
// has none; the longest matching name wins.
function resultRow(sent: { key: number; name: string }[], message: string): number | undefined {
  const named = (label: string) =>
    message.startsWith(`pre-registered result ${label} `) ||
    message.startsWith(`pre-registered result ${label}:`);
  const byName = sent
    .filter((row) => row.name && named(row.name))
    .sort((a, b) => b.name.length - a.name.length)[0];
  return byName?.key ?? sent.find((row, i) => !row.name && named(String(i + 1)))?.key;
}
onMounted(() => document.addEventListener('pointerdown', outside));
onUnmounted(() => document.removeEventListener('pointerdown', outside));
</script>
<template>
  <Modal
    title="New study"
    :class="['study-composer', 'manual', { expanded }]"
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
      <p v-if="duplicate" class="composer-warning" role="status">
        An active study already asks this: {{ number(duplicate.number) }}
        <template v-if="byId(duplicate.id)">“{{ byId(duplicate.id)!.title }}”</template>.
      </p>
      <div class="composer-properties">
        <div ref="chipRow" class="property-line" role="group" aria-label="Study properties">
          <AssignPicker
            ref="picker"
            v-model="assignee"
            v-model:range="range"
            one-agent
            :last-set="lastSet"
            @open="closeMenu"
            @picked="(last) => (usedLastSet = last)"
          />
          <div v-if="preregisters" class="property-anchor">
            <button
              type="button"
              :class="['property-pill', { required: !completeResults.length }]"
              :aria-expanded="menu === 'results'"
              :title="completeResults.length ? undefined : 'At least one result is needed'"
              @click="openMenu('results')"
            >
              <Icon name="check" :size="14" />Pre-registered · {{ completeResults.length }}
            </button>
            <section
              v-if="menu === 'results'"
              class="composer-menu results-menu"
              aria-label="Pre-registered results"
            >
              <div class="variables-header">
                <span>Pre-registered results</span>
                <button type="button" class="text-button" @click="addResult">Add result</button>
              </div>
              <p>
                Conditions on the numbers this study's scripts write, decided once and kept. At
                least one is needed.
              </p>
              <div class="variable-rows">
                <div
                  v-for="(row, index) in results"
                  :key="row.key"
                  role="group"
                  :aria-label="`Result ${index + 1}`"
                >
                  <div class="variable-row">
                    <input
                      v-model="row.name"
                      placeholder="Name"
                      maxlength="80"
                      :aria-label="`Result name ${index + 1}`"
                    />
                    <input
                      v-model="row.text"
                      placeholder="Condition, e.g. accuracy >= 0.9"
                      :aria-label="`Result condition ${index + 1}`"
                    />
                    <button
                      type="button"
                      class="text-button"
                      :aria-label="`Remove result ${index + 1}`"
                      @click="results = results.filter((r) => r.key !== row.key)"
                    >
                      Remove
                    </button>
                  </div>
                  <p v-if="resultError?.key === row.key" class="composer-error" role="alert">
                    {{ resultError.message }}
                  </p>
                </div>
              </div>
              <p v-if="resultKeys.length">
                The scripts will have to write: {{ resultKeys.join(', ') }}
              </p>
            </section>
          </div>
          <div v-if="folded < 2" class="property-anchor machine-chip">
            <MachinePicker v-model="machine" chip :disabled="busy" @open="closeMenu" />
          </div>
          <div v-if="folded < 1" class="property-anchor">
            <button type="button" class="property-pill" @click="openMenu('deadline')">
              <Icon name="clock" :size="14" />{{ deadline || 'No deadline' }}</button
            ><ComposerCalendar
              v-if="menu === 'deadline'"
              v-model="deadline"
              kind="due"
              @close="closeMenu"
            />
          </div>
          <div v-if="folded" class="property-anchor">
            <button
              type="button"
              class="property-pill more-pill"
              aria-label="More properties"
              :aria-expanded="menu === 'more' || (menu === 'deadline' && folded > 0)"
              @click="menu === 'deadline' ? closeMenu() : openMenu('more')"
            >
              ⋯
            </button>
            <div
              v-if="menu === 'more' || (menu === 'deadline' && folded > 0)"
              class="composer-menu fold-menu"
              role="group"
              aria-label="More properties"
            >
              <div v-if="folded > 1" class="machine-chip">
                <MachinePicker v-model="machine" chip :disabled="busy" />
              </div>
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
            </div>
          </div>
        </div>
      </div>
      <div v-if="files.items.value.length || files.error.value" class="composer-files">
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
      <p v-if="error" class="composer-error" role="alert">{{ error }}</p>
      <footer class="composer-footer">
        <label class="icon-button attachment-button" for="composer-files-input" :title="filesHint">
          <Icon name="attachment" :size="15" /><span class="visually-hidden">Attach files</span>
        </label>
        <input
          id="composer-files-input"
          type="file"
          multiple
          class="visually-hidden"
          aria-describedby="composer-files-hint"
          @change="pick"
        />
        <span id="composer-files-hint" class="visually-hidden">{{ filesHint }}</span>
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
<style scoped src="../../ui/composer-menu.css"></style>
<style scoped>
.study-composer {
  width: 578px;
  max-width: calc(100vw - 48px);
  background: var(--modal);
  overflow: visible;
}
.study-composer.manual {
  width: 800px;
}
.study-composer.expanded {
  width: min(1100px, calc(100vw - 64px));
}
.study-composer form {
  position: relative;
  display: flex;
  flex-direction: column;
  min-height: 308px;
}
.study-composer.manual form {
  min-height: 384px;
}
.study-composer.expanded form {
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
  gap: 6px;
  min-height: 26px;
  min-width: 0;
}
.property-line > *,
.property-line :deep(.property-anchor) {
  flex: none;
}
.property-line :deep(.property-pill),
.machine-chip :deep(.machine-trigger) {
  height: 26px;
  border: 1px solid var(--control-border);
  border-radius: 7px;
  background: transparent;
  color: var(--text);
}
.property-line :deep(.property-pill:hover:not(:disabled)),
.machine-chip :deep(.machine-trigger:hover:not(:disabled)) {
  background: var(--fill-chip);
}
.property-line .property-pill.required {
  border-color: var(--stage-experimentation-dot);
}
.machine-chip :deep(.machine-picker) {
  gap: 6px;
  white-space: nowrap;
}
.machine-chip :deep(.machine-trigger) {
  max-width: 160px;
  font-size: 12px;
}
.property-line :deep(.assignee-pill) {
  max-width: 180px;
}
.more-pill {
  padding: 0 10px;
}
/* Panels open upward and end at their chip, so they cover neither the footer
   nor the page's right edge. */
.results-menu,
.fold-menu {
  top: auto;
  bottom: calc(100% + 6px);
  left: auto;
  right: 0;
}
.results-menu {
  width: 460px;
  max-width: calc(100vw - 64px);
  padding: 10px;
  font-size: 12px;
}
.results-menu > p {
  margin: 4px 0 8px;
  padding: 0;
}
.fold-menu {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  min-width: 0;
  padding: 8px;
}
.fold-menu:has(:deep(.machine-menu)) {
  overflow: visible;
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
  outline: 2px solid var(--primary-text);
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
.study-relation-backdrop {
  position: absolute;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding-top: 32px;
}
.study-relation-picker {
  width: 386px;
  max-width: calc(100vw - 40px);
  background: var(--fill-menu);
  border: 1px solid #ffffff70;
  border-radius: 14px;
  padding: 8px;
  box-shadow: 0 12px 36px #0002;
}
.study-relation-picker header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.study-relation-picker h2 {
  font-size: 16px;
  margin: 0;
}
.study-relation-picker > p {
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
.composer-files {
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
.composer-files ul {
  list-style: none;
  margin: 0;
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
  outline: 2px solid var(--primary-text);
}
.composer-files .file-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
}
.composer-files .composer-error,
.results-menu .composer-error {
  padding: 0;
}
.attachment-button {
  cursor: pointer;
  color: var(--muted);
}
.composer-footer:has(#composer-files-input:focus-visible) .attachment-button {
  outline: 2px solid var(--primary-text);
}
.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
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
