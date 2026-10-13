<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import Icon from './components/Icon.vue';
import { History, MessageCircle } from '@lucide/vue';
import Modal from './components/Modal.vue';
import WorkspaceTabs from './components/WorkspaceTabs.vue';
import Aristotle from './components/Aristotle.vue';
import CreateTask from './views/CreateTask.vue';
import { state, workspaceName, refresh, notice, notify, service } from './services/workspace';
import { readText, writeText } from './storage';
import { usePins } from './state/pins';
import { useProfile } from './state/profile';
import { useTaskComposer } from './state/task-composer';
const route = useRoute();
const router = useRouter();
const current = computed(() => route.meta.page);
const taskId = computed(() => (current.value === 'tasks' ? route.params.taskId : undefined));
const groups = [
  {
    name: '',
    items: [
      { id: 'inbox', name: 'Inbox', icon: 'inbox' },
      { id: 'chat', name: 'Copilot', icon: 'chat' },
    ],
  },
  {
    name: 'Laboratory',
    items: [
      { id: 'tasks', name: 'Tasks', icon: 'tasks' },
      { id: 'projects', name: 'Researches', icon: 'folder' },
      { id: 'colleagues', name: 'Colleagues', icon: 'users' },
      { id: 'autopilots', name: 'Autopilot', icon: 'zap' },
    ],
  },
  {
    name: 'Configure',
    items: [
      { id: 'agents', name: 'Agents', icon: 'bot' },
      { id: 'squads', name: 'Teams', icon: 'users' },
      { id: 'skills', name: 'Skills', icon: 'book' },
      { id: 'connectors', name: 'Connectors', icon: 'plug' },
      { id: 'runtimes', name: 'Machines', icon: 'monitor' },
    ],
  },
];
const destinations = [
  ...groups.flatMap((g) => g.items),
  { id: 'updates', name: 'Updates', icon: 'download' },
  { id: 'settings', name: 'Settings', icon: 'settings' },
];
const heading = computed(() => route.meta.title ?? 'Tasks');
const tabTitle = computed(() =>
  taskId.value
    ? (state.data?.inquiries.find((x) => x.id === taskId.value)?.title ?? heading.value)
    : (route.meta.tabTitle ?? heading.value),
);
const headingIcon = computed(
  () => destinations.find((x) => x.id === current.value)?.icon ?? 'tasks',
);
const collapsed = ref(readText('lykeion.sidebar-collapsed') === 'true');
const { pins: pinnedIds } = usePins();
const pinnedExpanded = ref(readText('lykeion.pinned-expanded') !== 'false');
const pinnedTasks = computed(() =>
  pinnedIds.value
    .map((id) => state.data?.inquiries.find((t) => t.id === id))
    .filter((t): t is NonNullable<typeof t> => !!t),
);
watch(pinnedExpanded, (value) => writeText('lykeion.pinned-expanded', String(value)));
const { composer, openComposer: openCreate, closeComposer } = useTaskComposer();
const search = ref(false);
const query = ref('');
const workspaceMenu = ref(false);
const help = ref(false);
const aristotle = ref(false);
const desktop = Boolean(window.lykeionDesktop);
const workspacePopup = ref<HTMLElement>();
const helpPopup = ref<HTMLElement>();
const workspaceTrigger = ref<HTMLButtonElement>();
const helpTrigger = ref<HTMLButtonElement>();
const profile = useProfile();
const localProfile = computed(() => ({
  name: typeof profile.name === 'string' ? profile.name : '',
  avatar:
    typeof profile.avatar === 'string' && profile.avatar.startsWith('data:image/')
      ? profile.avatar
      : '',
}));
const feedback = ref(false);
const feedbackText = ref('');
function menuButtons(popup: HTMLElement | undefined) {
  return [...(popup?.querySelectorAll<HTMLButtonElement>('[role=menuitem]:not(:disabled)') || [])];
}
async function toggleWorkspace() {
  help.value = false;
  workspaceMenu.value = !workspaceMenu.value;
  if (workspaceMenu.value) {
    await nextTick();
    menuButtons(workspacePopup.value)[0]?.focus();
  }
}
async function toggleHelp() {
  workspaceMenu.value = false;
  help.value = !help.value;
  if (help.value) {
    await nextTick();
    menuButtons(helpPopup.value)[0]?.focus();
  }
}
function popupKey(
  event: KeyboardEvent,
  popup: HTMLElement | undefined,
  kind: 'workspace' | 'help',
) {
  const buttons = menuButtons(popup);
  let index = buttons.indexOf(document.activeElement as HTMLButtonElement);
  if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
    event.preventDefault();
    if (event.key === 'Home') index = 0;
    else if (event.key === 'End') index = buttons.length - 1;
    else index = (index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length;
    buttons[index]?.focus();
  }
  if (event.key === 'Escape') {
    event.preventDefault();
    event.stopPropagation();
    if (kind === 'workspace') {
      workspaceMenu.value = false;
      workspaceTrigger.value?.focus();
    } else {
      help.value = false;
      helpTrigger.value?.focus();
    }
  }
  if (event.key === 'Tab') {
    workspaceMenu.value = false;
    help.value = false;
  }
}
function outsidePopup(event: PointerEvent) {
  if (!(event.target as HTMLElement).closest('.shell-popup,[data-shell-menu-trigger]')) {
    workspaceMenu.value = false;
    help.value = false;
  }
}
async function documentation() {
  help.value = false;
  try {
    if (window.lykeionDesktop) await window.lykeionDesktop.openDocumentation();
    else window.open('https://github.com/lykeion-ia/lykeion', '_blank', 'noopener,noreferrer');
  } catch {
    notify('Documentation could not be opened.');
  }
}
async function copyFeedback() {
  try {
    await navigator.clipboard.writeText(feedbackText.value.trim());
    notify('Feedback copied. Nothing has been sent.');
  } catch {
    notify('Clipboard access was unavailable. You can select and copy the feedback.');
  }
}
const foundPages = computed(() =>
  (query.value ? destinations : []).filter((x) =>
    x.name.toLowerCase().includes(query.value.toLowerCase()),
  ),
);
const foundTasks = computed(() =>
  (
    state.data?.inquiries.filter((x) =>
      x.title.toLowerCase().includes(query.value.toLowerCase()),
    ) ?? []
  ).slice(0, 8),
);
const searchIndex = ref(0);
const searchEntries = computed(() => [
  { id: 'new', name: 'New', icon: 'plus' },
  ...foundTasks.value.map((x) => ({ id: 'tasks/' + x.id, name: x.title, icon: 'tasks' })),
  ...foundPages.value,
]);
watch(query, () => (searchIndex.value = 0));
function activateSearch(index: number) {
  const item = searchEntries.value[index];
  if (!item) return;
  if (item.id === 'new') {
    search.value = false;
    openCreate();
  } else go(item.id);
}
function searchKey(e: KeyboardEvent) {
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault();
    searchIndex.value =
      (searchIndex.value + (e.key === 'ArrowDown' ? 1 : -1) + searchEntries.value.length) %
      searchEntries.value.length;
  }
  if (e.key === 'Enter') {
    e.preventDefault();
    activateSearch(searchIndex.value);
  }
}
function go(id: string) {
  void router.push('/' + id);
  search.value = false;
  workspaceMenu.value = false;
  help.value = false;
}
function key(event: KeyboardEvent) {
  if (event.defaultPrevented) return;
  const editable = (event.target as HTMLElement).closest(
    'input, textarea, select, [contenteditable="true"]',
  );
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
    event.preventDefault();
    query.value = '';
    search.value = !search.value;
  }
  if ((event.metaKey || event.ctrlKey) && event.key === 'b') {
    event.preventDefault();
    collapsed.value = !collapsed.value;
  }
  if (
    !editable &&
    !event.metaKey &&
    !event.ctrlKey &&
    event.key.toLowerCase() === 'c' &&
    !document.querySelector('dialog[open]')
  ) {
    event.preventDefault();
    openCreate();
  }
  if (event.key === 'Escape') {
    workspaceMenu.value = false;
    help.value = false;
    aristotle.value = false;
  }
}
watch(collapsed, (x) => {
  writeText('lykeion.sidebar-collapsed', String(x));
  workspaceMenu.value = false;
  help.value = false;
});
watch(
  () => route.fullPath,
  () => {
    workspaceMenu.value = false;
    help.value = false;
  },
);
watch(
  heading,
  (x) => {
    document.title = `${x} | Lykeion`;
  },
  { immediate: true },
);
onMounted(() => {
  void refresh();
  window.addEventListener('keydown', key);
  window.addEventListener('pointerdown', outsidePopup);
});
onUnmounted(() => {
  window.removeEventListener('keydown', key);
  window.removeEventListener('pointerdown', outsidePopup);
});
</script>
<template>
  <div
    class="application"
    :class="{
      'sidebar-hidden': collapsed,
      desktop,
      'task-detail-open': Boolean(taskId),
    }"
  >
    <WorkspaceTabs
      :title="tabTitle"
      :icon="headingIcon"
      :collapsed="collapsed"
      @toggle="collapsed = !collapsed"
    />
    <aside class="sidebar" aria-label="Workspace navigation">
      <button
        ref="workspaceTrigger"
        class="workspace-switch nav-item"
        data-shell-menu-trigger
        aria-haspopup="menu"
        :aria-expanded="workspaceMenu"
        aria-controls="workspace-menu"
        @click="toggleWorkspace"
        @keydown.down.prevent="!workspaceMenu && toggleWorkspace()"
      >
        <span class="workspace-avatar">{{ workspaceName[0] }}</span
        ><span>{{ workspaceName }}</span
        ><Icon name="down" class="push-right" :size="14" />
      </button>
      <div
        v-if="workspaceMenu"
        id="workspace-menu"
        ref="workspacePopup"
        class="shell-popup workspace-popup"
        role="menu"
        aria-label="Workspace"
        @keydown="popupKey($event, workspacePopup, 'workspace')"
      >
        <button class="workspace-profile" role="menuitem" @click="go('settings?tab=profile')">
          <span class="profile-menu-avatar"
            ><img v-if="localProfile.avatar" :src="localProfile.avatar" alt="" /><template v-else>{{
              (localProfile.name || 'Local profile')[0]?.toUpperCase()
            }}</template></span
          ><span
            >{{ localProfile.name || 'Local profile'
            }}<small>{{
              service.mode === 'local' ? 'This device' : 'Account details unavailable'
            }}</small></span
          >
        </button>
        <div class="shell-menu-divider" />
        <small class="workspace-menu-label">Workspaces</small>
        <button role="menuitem" @click="go('tasks')">
          <span class="workspace-avatar">{{ workspaceName[0] }}</span
          >{{ workspaceName }}<Icon name="check" class="push-right" :size="14" />
        </button>
        <button role="menuitem" disabled title="Creating workspaces requires a workspace service">
          <Icon name="plus" :size="16" />Create workspace
        </button>
        <div class="shell-menu-divider" />
        <button
          role="menuitem"
          class="danger-menu-item"
          disabled
          :title="
            service.mode === 'local'
              ? 'This local workspace has no signed-in session'
              : 'Account sign-out is not available through this connection'
          "
        >
          <Icon name="logout" :size="16" />Log out
        </button>
      </div>
      <div class="quick-actions">
        <button
          class="nav-item"
          aria-label="Search..."
          @click="
            search = true;
            query = '';
          "
        >
          <Icon name="search" />Search...<span class="push-right shortcuts"
            ><kbd>⌘</kbd><kbd>K</kbd></span
          ></button
        ><button class="nav-item" aria-label="New" aria-keyshortcuts="C" @click="openCreate()">
          <Icon name="edit" />New<kbd class="push-right">C</kbd>
        </button>
      </div>
      <nav>
        <template v-for="(group, groupIndex) in groups" :key="group.name"
          ><section
            v-if="groupIndex === 1 && pinnedTasks.length"
            class="nav-group pinned-task-group"
          >
            <button
              class="nav-caption pinned-caption"
              :aria-label="`Pinned ${pinnedTasks.length}`"
              :aria-expanded="pinnedExpanded"
              @click="pinnedExpanded = !pinnedExpanded"
            >
              Pinned<Icon :name="pinnedExpanded ? 'down' : 'right'" :size="11" /></button
            ><template v-if="pinnedExpanded"
              ><RouterLink
                v-for="task in pinnedTasks"
                :key="task.id"
                :to="'/tasks/' + task.id"
                class="nav-item"
                :class="{ selected: taskId === task.id }"
                ><Icon
                  :name="task.status === 'done' ? 'check' : 'circle'"
                  :class="task.status === 'blocked' ? 'pinned-blocked' : ''"
                  :size="14"
                /><span>{{ task.title }}</span></RouterLink
              ></template
            >
          </section>
          <section class="nav-group">
            <div v-if="group.name" class="nav-caption">{{ group.name }}</div>
            <RouterLink
              v-for="item in group.items"
              :key="item.id"
              :to="'/' + item.id"
              class="nav-item"
              :class="{ selected: current === item.id }"
              :aria-current="current === item.id ? 'page' : undefined"
              ><Icon :name="item.icon" />{{ item.name }}</RouterLink
            >
          </section></template
        >
      </nav>
      <div class="sidebar-bottom">
        <RouterLink to="/updates" class="nav-item" :class="{ selected: current === 'updates' }"
          ><Icon name="download" />Updates</RouterLink
        >
        <div class="settings-line">
          <RouterLink to="/settings" class="nav-item" :class="{ selected: current === 'settings' }"
            ><Icon name="settings" />Settings</RouterLink
          ><button
            ref="helpTrigger"
            class="icon-button"
            aria-label="Help"
            data-shell-menu-trigger
            aria-haspopup="menu"
            :aria-expanded="help"
            aria-controls="help-menu"
            @click="toggleHelp"
            @keydown.up.prevent="!help && toggleHelp()"
          >
            <Icon name="help" />
          </button>
        </div>
      </div>
    </aside>
    <button
      class="sidebar-handle"
      aria-label="Toggle left sidebar"
      @click="collapsed = !collapsed"
    />
    <main class="main-pane">
      <header v-if="!['settings', 'chat', 'inbox'].includes(current ?? '')" class="page-header">
        <button
          v-if="collapsed"
          class="icon-button"
          aria-label="Toggle left sidebar"
          @click="collapsed = false"
        >
          <Icon name="sidebar" />
        </button>
        <h1>{{ heading }}</h1>
      </header>
      <div v-if="state.loading" class="center-empty" role="status">
        <Icon name="refresh" :size="28" /><span>Loading workspace…</span>
      </div>
      <div v-else-if="state.error" class="center-empty" role="alert">
        <Icon name="link" :size="32" />
        <p>{{ state.error }}</p>
        <button class="button" @click="refresh">Try again</button
        ><button class="text-button" @click="go('settings/connection')">Connection settings</button>
      </div>
      <RouterView v-slot="{ Component }">
        <component :is="Component" v-if="current === 'settings' && !state.loading" />
        <component
          :is="Component"
          v-else-if="
            !state.error && !state.loading && ['tasks', 'my-tasks'].includes(current ?? '')
          "
          @create="openCreate()"
        />
        <component
          :is="Component"
          v-else-if="!state.error && !state.loading"
          @create-task="openCreate()"
          @assign-agent="openCreate"
        />
      </RouterView>
      <RouterView v-if="taskId" name="detail" @close="go('tasks')" />
    </main>
    <button
      v-if="current !== 'chat' && !aristotle"
      class="aristotle-launcher"
      :aria-expanded="aristotle"
      aria-label="Open Aristotle"
      @click="aristotle = !aristotle"
    >
      <Aristotle />
    </button>
    <aside v-if="aristotle" class="running-composer" aria-label="Running task composer">
      <header>
        <button class="text-button" @click="notify('No tasks are running right now.')">
          Choose a running task <Icon name="down" :size="12" /></button
        ><button class="icon-button" aria-label="Close composer" @click="aristotle = false">
          <Icon name="close" />
        </button>
      </header>
      <Aristotle :portrait="false" />
      <p>No tasks are running right now.</p>
    </aside>
    <CreateTask
      v-if="composer.open"
      :initial-manual="composer.manual"
      :initial-assignee-id="composer.assigneeId"
      :initial-parent-id="composer.parentId"
      :initial-project-id="composer.projectId"
      @close="closeComposer()"
    />
    <Modal v-if="search" title="Search" @close="search = false"
      ><div class="search-box">
        <Icon name="search" :size="20" /><input
          v-model="query"
          autofocus
          placeholder="Type a command or search..."
          aria-label="Search workspace"
          role="combobox"
          aria-controls="search-options"
          :aria-activedescendant="'search-option-' + searchIndex"
          aria-expanded="true"
          @keydown="searchKey"
        /><kbd>Esc</kbd>
      </div>
      <div id="search-options" class="search-results" role="listbox">
        <template v-for="(item, index) in searchEntries" :key="item.id"
          ><small v-if="index === 0">Commands</small
          ><small v-else-if="index === 1">{{ query ? 'Results' : 'Recent' }}</small
          ><button
            :id="'search-option-' + index"
            role="option"
            :aria-selected="searchIndex === index"
            :class="{ highlighted: searchIndex === index }"
            @mousemove="searchIndex = index"
            @click="activateSearch(index)"
          >
            <Icon :name="item.icon" />{{ item.name }}
          </button></template
        >
        <p v-if="query && searchEntries.length === 1" class="muted">No results found.</p>
      </div></Modal
    >
    <div
      v-if="help"
      id="help-menu"
      ref="helpPopup"
      class="shell-popup help-popup"
      role="menu"
      aria-label="Help"
      @keydown="popupKey($event, helpPopup, 'help')"
    >
      <button role="menuitem" @click="documentation">
        <Icon name="book" />Docs<Icon name="external" :size="10" /></button
      ><button role="menuitem" @click="go('updates')">
        <History :size="16" :stroke-width="1.7" />Change log<Icon
          name="external"
          :size="10"
        /></button
      ><button role="menuitem" disabled title="A community invitation has not been configured">
        <Icon name="users" />Discord<Icon name="external" :size="10" /></button
      ><button
        role="menuitem"
        @click="
          help = false;
          feedback = true;
        "
      >
        <MessageCircle :size="16" :stroke-width="1.7" />Feedback
      </button>
      <div class="shell-menu-divider" />
      <small class="shell-version">Frontend version 0.1.0</small>
    </div>
    <Modal v-if="feedback" title="Feedback" @close="feedback = false"
      ><form class="feedback-form" @submit.prevent="copyFeedback">
        <header>
          <h2>Feedback</h2>
          <button
            class="icon-button"
            type="button"
            aria-label="Close feedback"
            @click="feedback = false"
          >
            <Icon name="close" />
          </button>
        </header>
        <p>
          Describe the problem or suggestion. You can copy this draft; feedback delivery is not
          configured.
        </p>
        <textarea
          v-model="feedbackText"
          autofocus
          aria-label="Feedback"
          placeholder="What would you like to improve?"
        />
        <footer>
          <button class="button" type="button" @click="feedback = false">Close</button
          ><button class="button primary" :disabled="!feedbackText.trim()">
            <Icon name="copy" :size="14" />Copy feedback
          </button>
        </footer>
      </form></Modal
    >
    <div v-if="notice" class="toast" role="status">
      {{ notice
      }}<button class="icon-button" aria-label="Dismiss notification" @click="notice = ''">
        <Icon name="close" :size="14" />
      </button>
    </div>
  </div>
</template>

<style scoped>
.shell-popup {
  position: fixed;
  z-index: 70;
  background: color-mix(in srgb, var(--input) 90%, var(--text) 10%);
  border: 1px solid color-mix(in srgb, var(--border) 75%, transparent);
  border-radius: 8px;
  box-shadow: 0 7px 22px #00000014;
  padding: 4px;
  color: var(--text);
  font-size: 14px;
  backdrop-filter: blur(12px);
}
.shell-popup > button {
  display: flex;
  align-items: center;
  gap: 7px;
  width: 100%;
  min-height: 28px;
  border-radius: 4px;
  padding: 4px 6px;
  text-align: left;
  font-size: 14px;
  line-height: 20px;
}
.shell-popup > button:hover:not(:disabled),
.shell-popup > button:focus-visible {
  background: color-mix(in srgb, var(--surface) 28%, transparent);
  outline: none;
}
.shell-popup > button:disabled {
  opacity: 0.5;
}
.shell-menu-divider {
  height: 1px;
  background: color-mix(in srgb, var(--surface) 35%, transparent);
  margin: 4px -4px;
}
.workspace-popup {
  left: 8px;
  top: 98px;
  width: 224px;
}
.workspace-popup > .workspace-profile {
  height: 44px;
  padding: 4px 12px;
  gap: 12px;
}
.workspace-profile > span:last-child {
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.workspace-profile small {
  display: block;
  font-size: 12px;
  color: var(--muted);
  line-height: 16px;
}
.profile-menu-avatar {
  width: 24px;
  height: 24px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  flex: none;
  font-size: 13px;
  overflow: hidden;
}
.profile-menu-avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.workspace-menu-label {
  display: block;
  font-size: 12px;
  line-height: 20px;
  color: var(--muted);
  padding: 0 6px 2px;
}
.workspace-popup .workspace-avatar {
  height: 18px;
  width: 18px;
  font-size: 11px;
  background: color-mix(in srgb, var(--surface) 30%, transparent);
}
.workspace-popup .push-right {
  color: var(--primary);
}
.shell-popup > .danger-menu-item {
  color: #cc344b;
}
.help-popup {
  left: 184px;
  bottom: 48px;
  width: 214px;
}
.help-popup > button > .icon:last-child:not(:first-child) {
  margin-left: 0;
  color: var(--muted);
}
.shell-version {
  display: block;
  font-size: 12px;
  line-height: 20px;
  color: var(--muted);
  padding: 0 5px 1px;
  white-space: nowrap;
}
.feedback-form {
  padding: 24px;
  display: grid;
  gap: 18px;
}
.feedback-form header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.feedback-form h2 {
  font-size: 18px;
}
.feedback-form p {
  font-size: 13px;
  line-height: 20px;
  color: var(--muted);
}
.feedback-form textarea {
  min-height: 150px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--surface);
  resize: vertical;
  padding: 10px;
  font: inherit;
  line-height: 22px;
}
.feedback-form footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
@media (max-width: 1750px) {
  .task-detail-open .aristotle-launcher {
    display: none;
  }
}
</style>

<style scoped>
.pinned-caption {
  display: flex;
  align-items: center;
  gap: 4px;
  width: 100%;
  text-align: left;
}
.pinned-task-group .nav-item > span {
  white-space: nowrap;
  text-overflow: ellipsis;
  overflow: hidden;
}
.pinned-blocked {
  color: #dc354d;
}
</style>
