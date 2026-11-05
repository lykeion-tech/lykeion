<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import Modal from '../../ui/Modal.vue';
import ComposerCalendar from './ComposerCalendar.vue';
import Icon from '../../ui/Icon.vue';
import AvatarDisplay from '../../ui/AvatarDisplay.vue';
import Aristotle from '../coresearch/Aristotle.vue';
import { api, ApiError } from '../../api/client';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import {
  uploadAttachment,
  useAgents,
  useCard,
  useCards,
  useMe,
  useResearches,
  useWorkspace,
} from '../../api/queries';
import { formatSize, useAttachments } from '../../lib/attachments';
import type { Card } from '../../api/types';
import { studyNumber, type Picked, type Range } from '../../lib/studies';
import { notify } from '../../state/notice';
import type { Assignee } from '../../state/study-composer';
import AssignPicker from '../../ui/AssignPicker.vue';
import { useWorkflows } from '../../api/workflows';
import { domainEmoji } from '../../lib/workflows';

const props = defineProps<{ assignee?: Assignee; projectId?: string; workflow?: string }>();
const emit = defineEmits<{ close: [] }>();
const client = useQueryClient();
const cards = useCards();
const workspace = useWorkspace();
const me = useMe();
const agents = useAgents();
const availableAgents = computed(() =>
  (agents.data.value ?? []).filter(
    (agent) =>
      !agent.archived_at &&
      (agent.visibility === 'workspace' || agent.owner_id === me.data.value?.id),
  ),
);
const projects = useResearches();
const isMac = navigator.platform.includes('Mac');
let generation = 0;
watch(
  workspaceId,
  () => {
    generation++;
    busy.value = false;
    error.value = '';
  },
  { flush: 'sync' },
);
onUnmounted(() => generation++);
const title = ref('');
const question = ref('');
const deadline = ref('');
const startDate = ref('');
const projectId = ref(props.projectId ?? '');
const selectedProject = computed(() =>
  projects.data.value?.find((project) => project.id === projectId.value),
);
const mode = ref<'manual' | 'agent'>('manual');
const agentPrompt = ref('');
const draftingAgent = ref(props.assignee?.type === 'agent' ? props.assignee.id : '');
const selectedDraftingAgent = computed(() =>
  availableAgents.value.find((agent) => agent.id === draftingAgent.value),
);
const expanded = ref(false);
const busy = ref(false);
const error = ref('');
const duplicate = ref<{ id: string; number: number } | null>(null);
const menu = ref('');
const form = ref<HTMLFormElement>();
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
const workflowSlug = ref(props.workflow ?? '');
const workflows = useWorkflows();
const workflowInfo = computed(() =>
  workflows.data.value?.workflows.find((w) => w.slug === workflowSlug.value),
);
const workflowRange: Range = { from: 'experimentation', to: 'experimentation' };
const range = ref<Range>(workflowSlug.value ? { ...workflowRange } : initialRange());
// Picking an agent resets the range; a Workflow keeps it on Experimentation.
watch(range, (r) => {
  if (workflowSlug.value && (r.from !== workflowRange.from || r.to !== workflowRange.to))
    range.value = { ...workflowRange };
});
// The Workflow needs one agent, so a Team picked before it is dropped.
if (workflowSlug.value && assignee.value?.type !== 'agent') assignee.value = null;
function dropWorkflow() {
  workflowSlug.value = '';
  range.value = initialRange();
}
const picker = ref<InstanceType<typeof AssignPicker>>();
const usedLastSet = ref(false);

const teamColumns = computed(() => picker.value?.teamColumns ?? []);
// A single Team with no set sends no range and works its own column.
const sendsRange = computed(
  () => !(assignee.value && teamColumns.value.length === 1 && !picker.value?.set),
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
function fitPopup(anchor: HTMLElement) {
  const popup = anchor.querySelector<HTMLElement>('.composer-menu, .composer-calendar');
  if (!popup) return;
  const bounds = anchor.getBoundingClientRect();
  popup.style.boxSizing = 'border-box';
  popup.style.maxHeight = `${Math.max(0, window.innerHeight - bounds.bottom - 14)}px`;
  popup.style.maxWidth = `${Math.max(0, window.innerWidth - 16)}px`;
  popup.style.overflowY = 'auto';
  popup.style.left = '0px';
  const right = popup.getBoundingClientRect().right;
  if (right > window.innerWidth - 8) popup.style.left = `${window.innerWidth - 8 - right}px`;
}
async function fitPropertyMenu(event: MouseEvent) {
  const anchor = (event.target as HTMLElement).closest<HTMLElement>('.property-anchor');
  if (!anchor) return;
  await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
  fitPopup(anchor);
}
function fitOpenPropertyMenus() {
  form.value?.querySelectorAll<HTMLElement>('.property-anchor').forEach(fitPopup);
}
function switchMode() {
  if (mode.value === 'manual') {
    if (assignee.value?.type !== 'agent') return;
    draftingAgent.value = assignee.value.id;
    mode.value = 'agent';
  } else {
    mode.value = 'manual';
  }
  closeMenu();
  error.value = '';
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
// A Workflow runs its method on the researcher's data.
const needsData = computed(
  () => mode.value === 'manual' && !!workflowSlug.value && files.ids.value.length === 0,
);
const blocked = computed(
  () =>
    busy.value ||
    (!!startDate.value && !!deadline.value && startDate.value > deadline.value) ||
    files.uploading.value ||
    files.failed.value ||
    (mode.value === 'agent'
      ? !agentPrompt.value.trim() || !draftingAgent.value || files.items.value.length > 0
      : !question.value.trim() ||
        unstaged.value ||
        (!!workflowSlug.value && assignee.value?.type !== 'agent') ||
        needsData.value),
);
async function submit() {
  if (blocked.value) return;
  const workspace = workspaceId.value;
  const visit = generation;
  const current = () => generation === visit && workspaceId.value === workspace;
  busy.value = true;
  error.value = '';
  duplicate.value = null;
  if (mode.value === 'agent') {
    try {
      await api(
        'POST',
        '/api/study-drafts',
        {
          agent_id: draftingAgent.value,
          prompt: agentPrompt.value.trim(),
          ...(projectId.value ? { research_id: projectId.value } : {}),
          start_date: startDate.value ? `${startDate.value}T00:00:00Z` : null,
          deadline: deadline.value ? new Date(`${deadline.value}T23:59:59`).toISOString() : null,
        },
        undefined,
        { workspace },
      );
      if (current()) {
        emit('close');
      }
    } catch (e) {
      if (current()) error.value = (e as Error).message;
    } finally {
      if (current()) busy.value = false;
    }
    return;
  }
  // No Team picked sends no assignee, and the built-in Teams work the range;
  // a single Team keeps its own stage, so the server takes no range for it,
  // while a Team set always runs its range.
  const fields = {
    ...(projectId.value ? { research_id: projectId.value } : {}),
    title: title.value.trim(),
    question: question.value.trim(),
    deadline: deadline.value ? new Date(`${deadline.value}T23:59:59`).toISOString() : null,
    start_date: startDate.value ? `${startDate.value}T00:00:00Z` : null,
    ...(files.ids.value.length ? { attachment_ids: files.ids.value } : {}),
    ...(assignee.value ? { assignee: assignee.value } : {}),
    ...(workflowSlug.value ? { workflow: workflowSlug.value } : {}),
    ...(sendsRange.value || workflowSlug.value ? { scope: range.value } : {}),
  };
  try {
    await api<Card>('POST', '/api/cards', fields, undefined, { workspace });
    if (!current()) return;
    if (projectId.value)
      await client.invalidateQueries({
        queryKey: keys.research(workspace, projectId.value),
      });
    await client.invalidateQueries({ queryKey: keys.cards(workspace) });
    if (!current()) return;
    notify('Study created.');
    emit('close');
  } catch (e) {
    if (!current()) return;
    const message = (e as Error).message;
    if (e instanceof ApiError && e.status === 409 && typeof e.body.card_id === 'string')
      duplicate.value = { id: e.body.card_id, number: Number(e.body.number) };
    else if (usedLastSet.value && /add this Team/.test((e as Error).message))
      error.value = "A Team in your last set isn't in your workspace any more";
    else error.value = (e as Error).message;
  } finally {
    if (current()) busy.value = false;
  }
}
onMounted(() => {
  document.addEventListener('pointerdown', outside);
  window.addEventListener('resize', fitOpenPropertyMenus);
});
onUnmounted(() => {
  document.removeEventListener('pointerdown', outside);
  window.removeEventListener('resize', fitOpenPropertyMenus);
});
</script>
<template>
  <Modal title="New study" :class="['study-composer', mode, { expanded }]" @close="emit('close')">
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
        <strong>{{ mode === 'agent' ? 'Create with agent' : 'New study' }}</strong>
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
          v-if="mode === 'manual'"
          v-model="title"
          autofocus
          class="composer-title"
          placeholder="Study title"
          aria-label="Study title"
        />
        <textarea
          v-if="mode === 'manual'"
          v-model="question"
          class="composer-description"
          placeholder="Describe what you want to do, in your own words"
          aria-label="Describe your study"
          required
        />
        <template v-else>
          <div class="draft-agent">
            <span>Created by</span>
            <span
              class="draft-agent-identity"
              :aria-label="`Created by agent: ${selectedDraftingAgent?.name || 'Agent'}`"
            >
              <span v-if="selectedDraftingAgent" class="draft-agent-avatar">
                <AvatarDisplay
                  v-if="selectedDraftingAgent.avatar_url"
                  :value="
                    selectedDraftingAgent.avatar_url.startsWith('emoji:')
                      ? selectedDraftingAgent.avatar_url.slice(6)
                      : selectedDraftingAgent.avatar_url
                  "
                  :size="16"
                />
                <Aristotle v-else-if="selectedDraftingAgent.built_in_key === 'aristotle'" still />
                <Icon
                  v-else-if="
                    selectedDraftingAgent.source === 'built_in' ||
                    selectedDraftingAgent.built_in_key
                  "
                  name="bot"
                  :size="16"
                />
                <span v-else>{{ selectedDraftingAgent.name[0] }}</span>
              </span>
              <span>{{ selectedDraftingAgent?.name || 'Agent' }}</span>
            </span>
          </div>
          <textarea
            v-model="agentPrompt"
            class="composer-prompt"
            placeholder="Describe the study you want an agent to create…"
            aria-label="Describe the study for the agent"
            maxlength="12000"
            required
          />
        </template>
      </div>
      <p v-if="mode === 'manual' && duplicate" class="composer-warning" role="status">
        An active study already asks this: {{ number(duplicate.number) }}
        <template v-if="byId(duplicate.id)">“{{ byId(duplicate.id)!.title }}”</template>.
      </p>
      <div class="composer-properties">
        <div class="property-line" role="group" aria-label="Study properties" @click.capture="fitPropertyMenu">
          <AssignPicker
            v-if="mode === 'manual'"
            ref="picker"
            v-model="assignee"
            v-model:range="range"
            one-agent
            default-label="Assignees"
            :agent-only="!!workflowSlug"
            :last-set="lastSet"
            menu-below
            @open="closeMenu"
            @picked="(last) => (usedLastSet = last)"
          >
          </AssignPicker>
          <span v-if="mode === 'manual' && workflowSlug" class="property-pill workflow-chip">
            {{ workflowInfo ? domainEmoji(workflowInfo.domain) : '🧪' }} Workflow:
            {{ workflowInfo?.name ?? workflowSlug }}
            <button
              type="button"
              class="text-button"
              aria-label="Remove workflow"
              @click="dropWorkflow"
            >
              <Icon name="close" :size="12" />
            </button>
          </span>
          <div class="property-anchor">
            <button
              type="button"
              class="property-pill"
              title="Planning date; the study starts when created"
              :aria-expanded="menu === 'start'"
              @click="openMenu('start')"
            >
              <Icon name="calendar" :size="14" />{{ startDate || 'Start date' }}
            </button>
            <ComposerCalendar
              v-if="menu === 'start'"
              v-model="startDate"
              kind="start"
              @close="closeMenu"
            />
          </div>
          <div class="property-anchor">
            <button
              type="button"
              class="property-pill"
              :aria-expanded="menu === 'deadline'"
              @click="openMenu('deadline')"
            >
              <Icon name="calendar" :size="14" />{{ deadline || 'Due date' }}
            </button>
            <ComposerCalendar
              v-if="menu === 'deadline'"
              v-model="deadline"
              kind="due"
              @close="closeMenu"
            />
          </div>
          <div class="property-anchor">
            <button
              type="button"
              class="property-pill project-pill"
              :aria-expanded="menu === 'project'"
              @click="openMenu('project')"
            >
              <Icon name="folder" :size="14" /><span>{{
                selectedProject?.name || 'No project'
              }}</span>
            </button>
            <div
              v-if="menu === 'project'"
              class="composer-menu project-menu"
              role="menu"
              aria-label="Choose project"
            >
              <button
                type="button"
                role="menuitemradio"
                :aria-checked="!projectId"
                @click="
                  projectId = '';
                  closeMenu();
                "
              >
                No project
              </button>
              <button
                v-for="project in projects.data.value ?? []"
                :key="project.id"
                type="button"
                role="menuitemradio"
                :aria-checked="projectId === project.id"
                @click="
                  projectId = project.id;
                  closeMenu();
                "
              >
                {{ project.name }}
              </button>
              <p v-if="projects.isError.value" role="alert">Could not load projects.</p>
            </div>
          </div>
        </div>
      </div>
      <p v-if="startDate && deadline && startDate > deadline" class="composer-error" role="alert">
        Start date must be on or before the due date.
      </p>
      <p v-if="mode === 'agent' && files.items.value.length" class="composer-warning" role="status">
        Attachments are kept in manual mode. Remove them there before sending a request to an agent.
      </p>
      <p v-if="mode === 'agent' && workflowSlug" class="composer-warning" role="status">
        The selected workflow is kept in manual mode and will not be included in the agent request.
      </p>
      <p v-if="needsData" class="composer-warning" role="status">
        A Workflow runs its method on your data: attach at least one file.
      </p>
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
        <button
          type="button"
          class="mode-switch push-right"
          :aria-pressed="mode === 'agent'"
          :disabled="
            busy || files.uploading.value || (mode === 'manual' && assignee?.type !== 'agent')
          "
          :title="
            mode === 'manual' && assignee?.type !== 'agent'
              ? 'Choose an agent assignee first'
              : undefined
          "
          @click="switchMode"
        >
          <span
            ><Icon name="switch" :size="14" />{{
              mode === 'manual' ? 'Switch to Agent' : 'Switch to Manual'
            }}</span
          >
        </button>
        <button type="submit" class="button primary compact" :disabled="blocked">
          {{ busy ? 'Sending…' : mode === 'agent' ? 'Create' : 'Create study'
          }}<kbd>{{ isMac ? '⌘' : 'Ctrl' }}</kbd> <kbd>↵</kbd>
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
.study-composer.manual,
.study-composer.agent {
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
.study-composer.manual form,
.study-composer.agent form {
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
.draft-agent {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 18px;
  font-size: 12px;
  color: var(--muted);
}
.draft-agent-identity {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--text);
}
.draft-agent-avatar {
  width: 20px;
  height: 20px;
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  border-radius: 50%;
  background: var(--fill-chip);
  font-size: 11px;
}
.draft-agent-avatar :deep(.aristotle-art) {
  width: 20px;
  height: 20px;
}
.mode-switch {
  position: relative;
  display: inline-flex;
  overflow: hidden;
  padding: 1px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  cursor: pointer;
}
.mode-switch::before {
  content: '';
  position: absolute;
  inset: -180%;
  background: conic-gradient(
    transparent 0 68%,
    #4b8dd6 74%,
    #b78be6 82%,
    #f09983 88%,
    transparent 94%
  );
  animation: mode-switch-orbit 4s linear infinite;
}
.mode-switch > span {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 9px;
  border-radius: 5px;
  color: var(--text);
  background: var(--modal);
  font-size: 12px;
}
.mode-switch:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: 2px;
}
.mode-switch:disabled {
  opacity: 0.5;
  cursor: default;
}
@keyframes mode-switch-orbit {
  to {
    transform: rotate(360deg);
  }
}
@media (prefers-reduced-motion: reduce) {
  .mode-switch::before {
    animation: none;
  }
}
.composer-body {
  display: flex;
  flex-direction: column;
  padding: 0 20px;
  flex: 1;
  min-height: 166px;
}
.manual .composer-body,
.agent .composer-body {
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
  position: relative;
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  min-height: 26px;
  min-width: 0;
}
.property-line > *,
.property-line :deep(.property-anchor) {
  flex: none;
}
.property-line :deep(.property-pill) {
  height: 26px;
  border: 1px solid var(--control-border);
  border-radius: 7px;
  background: transparent;
  color: var(--text);
}
.property-line :deep(.property-pill > .icon) {
  transform: translateY(-1px);
}
.property-line :deep(.property-pill:hover:not(:disabled)) {
  background: var(--fill-chip);
}
.property-line .property-pill.required {
  border-color: var(--stage-experimentation-dot);
}
.property-line :deep(.assignee-pill) {
  max-width: 180px;
  border-radius: 7px;
}
.project-pill {
  max-width: 180px;
}
.project-pill span {
  overflow: hidden;
  text-overflow: ellipsis;
}
.project-menu {
  top: calc(100% + 6px);
  bottom: auto;
  min-width: 180px;
  max-height: 240px;
  overflow-y: auto;
  padding: 6px;
}
.property-line :deep(.composer-calendar) {
  top: calc(100% + 6px);
  bottom: auto;
}
.project-menu button {
  display: block;
  width: 100%;
  padding: 7px 8px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--text);
  text-align: left;
  cursor: pointer;
}
.project-menu button:hover,
.project-menu button[aria-checked='true'] {
  background: var(--fill-chip);
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
  .mode-switch > span {
    font-size: 11px;
    padding: 4px;
  }
}
.composer-files {
  padding: 0 20px 8px;
  font-size: 12px;
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
.composer-files .composer-error {
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
