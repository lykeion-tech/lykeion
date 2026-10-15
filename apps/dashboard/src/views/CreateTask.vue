<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Modal from '../components/Modal.vue';
import ComposerCalendar from '../components/ComposerCalendar.vue';
import Icon from '../components/Icon.vue';
import { api, ApiError } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import { useAgents, useCards, useMe, useMembers, useWorkspace } from '../api/queries';
import type { Card } from '../api/types';
import { dateKey } from '../lib/dates';
import { matchesQuery } from '../lib/search';
import { studyNumber } from '../lib/studies';
import { notify } from '../state/notice';
import type { Assignee } from '../state/task-composer';

const props = defineProps<{ editId?: string; assignee?: Assignee }>();
const emit = defineEmits<{ close: [] }>();
const client = useQueryClient();
const cards = useCards();
const workspace = useWorkspace();
const members = useMembers();
const agents = useAgents();
const me = useMe();
const isMac = navigator.platform.includes('Mac');
const title = ref('');
const question = ref('');
const budget = ref(0);
const deadline = ref('');
const followsFrom = ref<string[]>([]);
const originalFollowsFrom = ref<string[]>([]);
const another = ref(false);
const expanded = ref(false);
const busy = ref(false);
const error = ref('');
const duplicate = ref<{ id: string; number: number } | null>(null);
const menu = ref('');
const query = ref('');
const form = ref<HTMLFormElement>();
const assignee = ref<Assignee | null>(props.assignee ? { ...props.assignee } : null);

// The creator owns the study, so a private agent is offered only to its owner.
const assignableAgents = computed(() =>
  (agents.data.value ?? []).filter(
    (a) => !a.archived_at && (a.visibility === 'workspace' || a.owner_id === me.data.value?.id),
  ),
);
const memberName = (id: string) => {
  const m = members.data.value?.find((x) => x.user_id === id);
  return m ? m.name || m.email : 'Member';
};
const assigneeName = computed(() =>
  !assignee.value
    ? ''
    : assignee.value.type === 'agent'
      ? (agents.data.value?.find((a) => a.id === assignee.value!.id)?.name ?? 'Agent')
      : memberName(assignee.value.id),
);
const matchingMembers = computed(() =>
  (members.data.value ?? []).filter((m) => matchesQuery(`${m.name} ${m.email}`, query.value)),
);
const matchingAgents = computed(() =>
  assignableAgents.value.filter((a) => matchesQuery(a.name, query.value)),
);
function assign(value: typeof assignee.value) {
  assignee.value = value;
  closeMenu();
}

const editing = computed(() => cards.data.value?.find((c) => c.id === props.editId));
const heading = computed(() => (props.editId ? 'Edit study' : 'New study'));
const number = (n: number) => studyNumber(workspace.data.value, { number: n });
const byId = (id: string) => cards.data.value?.find((c) => c.id === id);
const candidates = computed(() =>
  (cards.data.value ?? []).filter(
    (c) =>
      c.id !== props.editId &&
      !followsFrom.value.includes(c.id) &&
      matchesQuery(`${number(c.number)} ${c.title}`, query.value),
  ),
);

// An edited study starts from what it holds now.
watch(
  editing,
  (c: Card | undefined) => {
    if (!c || title.value || question.value) return;
    title.value = c.title;
    question.value = c.question;
    budget.value = c.budget_usd;
    deadline.value = c.deadline ? dateKey(new Date(c.deadline)) : '';
    followsFrom.value = [...c.follows_from];
    originalFollowsFrom.value = [...c.follows_from];
  },
  { immediate: true },
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

async function submit() {
  if (busy.value || !question.value.trim()) return;
  busy.value = true;
  error.value = '';
  duplicate.value = null;
  const fields = {
    title: title.value.trim(),
    question: question.value.trim(),
    budget_usd: Math.max(0, Number(budget.value) || 0),
    deadline: deadline.value ? new Date(`${deadline.value}T23:59:59`).toISOString() : null,
    ...(!props.editId ||
    JSON.stringify(followsFrom.value) !== JSON.stringify(originalFollowsFrom.value)
      ? { follows_from: followsFrom.value }
      : {}),
  };
  try {
    if (props.editId) await api('PATCH', `/api/cards/${props.editId}`, fields);
    else {
      const created = await api<Card>('POST', '/api/cards', fields);
      // ponytail: two requests, so a refused assignment leaves the study created unassigned; fold into POST /api/cards if that matters.
      if (assignee.value)
        await api('PATCH', `/api/cards/${created.id}/properties`, {
          assignee_type: assignee.value.type,
          assignee_id: assignee.value.id,
        }).catch((e: Error) => notify(`Study created, but not assigned: ${e.message}`));
    }
    await client.invalidateQueries({ queryKey: keys.cards(workspaceId.value) });
    if (props.editId) {
      await client.invalidateQueries({ queryKey: keys.card(workspaceId.value, props.editId) });
      notify('Study saved.');
      emit('close');
      return;
    }
    notify('Study created.');
    if (another.value) {
      title.value = question.value = deadline.value = '';
      budget.value = 0;
      assignee.value = props.assignee ? { ...props.assignee } : null;
      followsFrom.value = [];
      await nextTick();
      form.value?.querySelector<HTMLInputElement>('.composer-title')?.focus();
    } else emit('close');
  } catch (e) {
    if (e instanceof ApiError && e.status === 409 && typeof e.body.card_id === 'string')
      duplicate.value = { id: e.body.card_id, number: Number(e.body.number) };
    else error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
onMounted(() => document.addEventListener('pointerdown', outside));
onUnmounted(() => document.removeEventListener('pointerdown', outside));
</script>
<template>
  <Modal :title="heading" :class="['task-composer', 'manual', { expanded }]" @close="emit('close')">
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
        <strong>{{ heading }}</strong>
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
          placeholder="What is the research question?"
          aria-label="Research question"
          required
        />
      </div>
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
          <div class="property-anchor">
            <button
              type="button"
              class="property-pill"
              disabled
              title="New studies start in Backlog"
            >
              <Icon name="circle" :size="14" />Backlog
            </button>
          </div>
          <div class="property-anchor">
            <button type="button" class="property-pill" @click="openMenu('budget')">
              {{ budget > 0 ? `$${budget} budget` : 'No budget limit' }}
            </button>
            <div v-if="menu === 'budget'" class="composer-menu" role="dialog" aria-label="Budget">
              <label
                >Budget limit (USD)<input
                  v-model.number="budget"
                  type="number"
                  min="0"
                  step="0.5"
                  aria-label="Budget limit in USD"
              /></label>
              <p>0 means no budget limit.</p>
            </div>
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
          <div v-if="!editId" class="property-anchor">
            <button
              type="button"
              class="property-pill assignee-pill"
              :aria-label="assignee ? `Assignee: ${assigneeName}` : 'Unassigned'"
              @click="openMenu('assignee')"
            >
              <span v-if="assignee" class="avatar">{{ assigneeName[0] }}</span
              ><span>{{ assignee ? assigneeName : 'Unassigned' }}</span>
            </button>
            <div v-if="menu === 'assignee'" class="composer-menu" role="menu">
              <input v-model="query" placeholder="Assign to…" aria-label="Search assignees" />
              <button type="button" role="menuitem" @click="assign(null)">Unassigned</button>
              <p v-if="matchingMembers.length" class="menu-group">Members</p>
              <button
                v-for="m in matchingMembers"
                :key="m.user_id"
                type="button"
                role="menuitem"
                @click="assign({ type: 'member', id: m.user_id })"
              >
                <span class="avatar">{{ (m.name || m.email)[0] }}</span
                ><span>{{ m.name || m.email }}</span>
              </button>
              <p v-if="matchingAgents.length" class="menu-group">Agents</p>
              <button
                v-for="a in matchingAgents"
                :key="a.id"
                type="button"
                role="menuitem"
                @click="assign({ type: 'agent', id: a.id })"
              >
                <Icon name="bot" :size="14" /><span>{{ a.name }}</span>
              </button>
              <p v-if="!matchingMembers.length && !matchingAgents.length">No matches</p>
            </div>
          </div>
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
      <p v-if="error" class="composer-error" role="alert">{{ error }}</p>
      <footer class="composer-footer">
        <label v-if="!editId" class="another-switch push-right">
          <input v-model="another" type="checkbox" role="switch" aria-label="Create another" />
          <span class="switch-track" />Create another</label
        >
        <button
          type="submit"
          class="button primary compact"
          :class="{ 'push-right': editId }"
          :disabled="busy || !question.trim()"
        >
          {{ busy ? 'Saving…' : editId ? 'Save study' : 'Create study'
          }}<kbd>{{ isMac ? '⌘' : 'Ctrl' }}</kbd> <kbd>↵</kbd>
        </button>
      </footer>
    </form>
  </Modal>
</template>
<style scoped>
.task-composer {
  width: 578px;
  max-width: calc(100vw - 48px);
  background: var(--fill-menu);
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
.property-anchor {
  position: relative;
}
.property-pill {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 26px;
  padding: 0 10px;
  border: 1px solid #ffffff42;
  border-radius: 18px;
  font-size: 12px;
  color: var(--muted);
  white-space: nowrap;
  max-width: 240px;
}
.property-pill:hover {
  background: #ffffff30;
}
.assignee-pill > span {
  overflow: hidden;
  text-overflow: ellipsis;
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
.composer-menu {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  z-index: 70;
  min-width: 250px;
  max-width: 340px;
  max-height: 290px;
  overflow: auto;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 5px;
  box-shadow: 0 6px 22px #0002;
  color: var(--text);
  font-size: 13px;
}
.composer-menu > input {
  width: 100%;
  background: none;
  border: 0;
  border-bottom: 1px solid var(--border);
  outline: 0;
  padding: 9px 8px;
  margin-bottom: 4px;
  font-size: 13px;
}
.composer-menu > button {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 31px;
  padding: 6px 8px;
  text-align: left;
  border-radius: 4px;
  font-size: 13px;
}
.composer-menu > button:hover,
.composer-menu > button:focus-visible {
  background: var(--input);
}
.composer-menu small {
  margin-left: auto;
  color: var(--muted);
  font-size: 11px;
}
.composer-menu p.menu-group {
  padding: 6px 8px 2px;
  font-size: 11px;
}
.composer-menu p {
  padding: 8px;
  color: var(--muted);
  font-size: 12px;
  line-height: 18px;
  white-space: normal;
}
.composer-menu label {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px;
}
.composer-menu label input {
  background: var(--input);
  padding: 8px;
  border: 1px solid var(--border);
  border-radius: 5px;
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
.stage-menu {
  bottom: calc(100% + 6px);
  top: auto;
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
