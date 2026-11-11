<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, provide, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useQueryClient } from '@tanstack/vue-query';
import { MessageCircle } from '@lucide/vue';
import Icon from './ui/Icon.vue';
import SidebarFooter from './ui/SidebarFooter.vue';
import { usePaneResize } from './lib/resize';
import { studyDisplayTitle } from './lib/studies';
import Modal from './ui/Modal.vue';
import WorkspaceTabs from './features/workspace/WorkspaceTabs.vue';
import CommandPalette from './ui/CommandPalette.vue';
import ConversationSearch from './features/coresearch/ConversationSearch.vue';
import DecisionSearch from './features/decisions/DecisionSearch.vue';
import CreateStudy from './features/studies/CreateStudy.vue';
import { api, ApiError } from './api/client';
import { selectWorkspace, workspaceId } from './api/session';
import DiscardDraftDialog from './features/workspace/settings/library/DiscardDraftDialog.vue';
import { guardResourceDrafts } from './state/resource-draft';
import { useCards, useMe, useWorkspace, useWorkspaces } from './api/queries';
import { useRealtimeSync } from './api/realtime';
import { notice, notify } from './state/notice';
import { readText, writeText } from './storage';
import { usePins } from './state/pins';
import { useStudyComposer } from './state/study-composer';
import { createDaemonSession, daemonSessionKey } from './lib/daemon-session';
import { keys } from './api/keys';
import { useCoresearchFiles, useCoresearchProject } from './features/coresearch/api';
import { useAristotle } from './features/coresearch/aristotle';
import ConversationRail from './features/coresearch/ConversationRail.vue';
import SidebarBrand, { brandSwitches } from './ui/SidebarBrand.vue';
import AristotleWindow from './features/aristotle-window/AristotleWindow.vue';
import { shownReport } from './features/reports/shown-report';
import DecisionRail from './features/decisions/DecisionRail.vue';
const labWindow = ref<InstanceType<typeof AristotleWindow>>();
const route = useRoute();
const router = useRouter();
const currentCoresearchProject = useCoresearchProject(() => String(route.params.projectId ?? ''));
const artifactFiles = useCoresearchFiles(() =>
  route.name === 'coresearch-artifact' ? String(route.params.projectId ?? '') : '',
);
const artifactName = computed(() =>
  artifactFiles.data.value
    ?.find((f) => f.id === route.params.fileId)
    ?.path.split('/')
    .pop(),
);
const cowriteShell = computed(
  () =>
    route.name !== 'coresearch-artifact' && currentCoresearchProject.data.value?.mode === 'cowrite',
);
const client = useQueryClient();
const me = useMe();
const aristotle = useAristotle();
const aristotleShell = computed(() => !!route.meta.conversationRail);
const inboxShell = computed(() => route.meta.page === 'decisions');
const railStorageKey = computed(() =>
  inboxShell.value
    ? `lykeion.decisions.rail:${workspaceId.value}`
    : `lykeion.aristotle.rail:${aristotle.scope.value}`,
);
const railView = ref<'nav' | 'context'>('context');
watch(
  railStorageKey,
  (key) => {
    railView.value =
      inboxShell.value || aristotleShell.value
        ? 'context'
        : readText(key) === 'nav'
          ? 'nav'
          : 'context';
  },
  { immediate: true },
);
function changeRail(view: 'nav' | 'context') {
  railView.value = view;
  collapsed.value = false;
  writeText(railStorageKey.value, view);
}
const workspaces = useWorkspaces();
const workspace = useWorkspace();
const cards = useCards();
const { reconnecting, lost } = useRealtimeSync(workspaceId, client);
const unauthorized = computed(() =>
  [me.error.value, workspaces.error.value, workspace.error.value, cards.error.value].some(
    (error) => error instanceof ApiError && error.status === 401,
  ),
);
const desktopSession = createDaemonSession({
  bridge: window.lykeionDesktop?.daemon,
  // eslint-disable-next-line no-undef -- injected by the dashboard Vite configuration
  apiOrigin: __LYK_PUBLIC_API_ORIGIN__,
  token: () => api<{ token: string }>('POST', '/api/cli-token'),
  reportError: notify,
  onConfigured: () => {
    if (workspaceId.value)
      void client.invalidateQueries({ queryKey: keys.runtimes(workspaceId.value) });
  },
});
provide(daemonSessionKey, desktopSession);
watch(
  [() => me.data.value?.id, unauthorized, me.dataUpdatedAt],
  ([id, signedOut]) => {
    void desktopSession.update(id, signedOut);
  },
  { immediate: true, flush: 'sync' },
);
onUnmounted(desktopSession.dispose);

// A cached refusal still protects navigation after the request has settled.
watch(
  [() => route.fullPath, unauthorized],
  ([, signedOut]) => {
    if (!signedOut || route.meta.public) return;
    if (route.params.token) writeText('lykeion.invite', String(route.params.token), 'session');
    void router.replace('/signin');
  },
  { immediate: true },
);

const shellQueries = [me, workspaces, workspace];
const shellFailure = computed(
  () =>
    shellQueries.find((query) => query.data.value === undefined && query.isError.value)?.error
      .value,
);
const shellPending = computed(
  () =>
    me.data.value === undefined ||
    workspaces.data.value === undefined ||
    (Boolean(workspaceId.value) && workspace.data.value === undefined),
);
const shellRetrying = computed(() => shellQueries.some((query) => query.isFetching.value));
async function retryShell() {
  await Promise.all(
    shellQueries
      .filter((query) => query.data.value === undefined && query.isError.value)
      .map((query) => query.refetch()),
  );
}
const current = computed(() => route.meta.page);
const cardId = computed(() =>
  ['studies', 'my-studies'].includes(current.value ?? '') ? String(route.params.cardId || '') : '',
);
const groups = [
  {
    name: '',
    items: [
      { id: 'autoresearch', name: 'Autoresearch', icon: 'zap' },
      { id: 'my-studies', name: 'My Studies', icon: 'tasks' },
    ],
  },
  {
    name: 'Laboratory',
    items: [
      { id: 'studies', name: 'Studies', icon: 'tasks' },
      { id: 'projects', name: 'Projects', icon: 'folder' },
      { id: 'colleagues', name: 'Colleagues', icon: 'users' },
    ],
  },
  {
    name: 'IT Department',
    items: [
      { id: 'teams', name: 'Teams', icon: 'team' },
      { id: 'workflows', name: 'Workflows', icon: 'flask' },
      { id: 'agents', name: 'Agents', icon: 'bot' },
    ],
  },
];
const destinations = [
  ...groups.flatMap((g) => g.items),
  ...brandSwitches,
  { id: 'lab-chart', name: 'Lykeion', icon: 'network' },
  { id: 'settings', name: 'Settings', icon: 'settings' },
];
const workspaceName = computed(() => workspace.data.value?.name ?? 'Lykeion');
const heading = computed(() =>
  current.value === 'teams' ? 'Teams' : (route.meta.title ?? 'Studies'),
);
const shownCard = computed(() => cards.data.value?.find((c) => c.id === cardId.value));
// Arriving at a study's report opens Aristotle once, with the report as its
// context; closing it holds until the next arrival. Leaving the report
// collapses the window again if the report was what opened it.
let reportOpened = '';
let openedByReport = false;
watch([() => shownReport.value?.id ?? '', labWindow], ([id, win]) => {
  if (!id) {
    if (openedByReport) win?.close();
    openedByReport = false;
    reportOpened = '';
  } else if (win && reportOpened !== id) {
    // Moving from one report to another keeps what the first one decided.
    if (!reportOpened) openedByReport = !win.isOpen();
    reportOpened = id;
    win.open();
  }
});
const tabTitle = computed(() =>
  shownCard.value
    ? `${workspace.data.value?.display_prefix ?? ''}-${shownCard.value.number}: ${studyDisplayTitle(shownCard.value.title)}`
    : route.name === 'coresearch-artifact'
      ? (artifactName.value ?? 'File')
      : cowriteShell.value
        ? (currentCoresearchProject.data.value?.name ?? '')
        : (route.meta.tabTitle ?? heading.value),
);
const headingIcon = computed(
  () => destinations.find((x) => x.id === current.value)?.icon ?? 'tasks',
);
const rail = usePaneResize('lykeion.sidebar-width', 262, 219, 360);
const collapsed = ref(readText('lykeion.sidebar-collapsed') === 'true');
// Entering either workspace starts with its own history; the toggle remains
// available while navigating within that workspace.
watch(
  () => (inboxShell.value ? 'decisions' : aristotleShell.value ? 'coresearch' : ''),
  (shell, previous) => {
    if (shell && shell !== previous) {
      changeRail('context');
      if (window.matchMedia?.('(max-width: 700px)').matches) collapsed.value = true;
    }
  },
  { immediate: true, flush: 'post' },
);
const { pins: pinnedIds } = usePins();
const pinnedExpanded = ref(readText('lykeion.pinned-expanded') !== 'false');
const pinnedCards = computed(() =>
  pinnedIds.value
    .map((id) => cards.data.value?.find((c) => c.id === id))
    .filter((c): c is NonNullable<typeof c> => !!c),
);
watch(pinnedExpanded, (value) => writeText('lykeion.pinned-expanded', String(value)));
const { composer, openComposer: openStudyCreate, closeComposer } = useStudyComposer();
function openCreate() {
  openStudyCreate();
}
const workspaceMenu = ref(false);
const help = ref(false);
const desktop = Boolean(window.lykeionDesktop);
const workspacePopup = ref<HTMLElement>();
const helpPopup = ref<HTMLElement>();
const sidebarFooter = ref<InstanceType<typeof SidebarFooter>>();
const feedback = ref(false);
const paletteOpen = ref(false);
// A rail that lists conversations or decisions searches those; every other
// view searches researches and studies.
const paletteScope = computed(() =>
  railView.value !== 'context'
    ? 'studies'
    : inboxShell.value
      ? 'decisions'
      : aristotleShell.value
        ? 'conversations'
        : 'studies',
);
function openSearch() {
  workspaceMenu.value = false;
  help.value = false;
  paletteOpen.value = true;
}
watch(
  workspaceId,
  () => {
    paletteOpen.value = false;
  },
  { flush: 'sync' },
);
const feedbackText = ref('');

// The shown workspace must be one the person belongs to; otherwise the first.
watch(
  () => workspaces.data.value,
  (list) => {
    if (list && !list.some((w) => w.id === workspaceId.value))
      void selectWorkspace(list[0]?.id ?? '');
  },
  { immediate: true },
);
// The socket refused the workspace (removed, or deleted): pick another and show the switcher.
async function recoverWorkspace() {
  if (!(await selectWorkspace(''))) return;
  void client.invalidateQueries({ queryKey: ['workspaces'] });
  workspaceMenu.value = true;
}
watch(lost, (gone) => {
  if (gone) recoverWorkspace();
});
// The shell must stay unmounted until the onboarding redirect finishes, so its
// restored tab cannot navigate over the wizard.
const onboardingTarget = computed(() => {
  const m = me.data.value;
  const list = workspaces.data.value;
  if (!m || !list) return '';
  if (!m.onboarded_at) return '/onboarding';
  return list.length ? '' : '/onboarding?step=workspace';
});
watch(
  [onboardingTarget, () => route.meta.bare, unauthorized],
  ([target, bare, signedOut]) => {
    if (target && !bare && !signedOut) void router.replace(target);
  },
  { immediate: true },
);

watch(
  () => workspace.error.value,
  (error) => {
    if (error instanceof ApiError && [403, 404].includes(error.status)) recoverWorkspace();
  },
);
async function chooseWorkspace(id: string) {
  if (!(await selectWorkspace(id))) return;
  workspaceMenu.value = false;
  void router.push('/studies');
}
async function logOut() {
  if (
    !(await guardResourceDrafts(async () => {
      try {
        await desktopSession.clear();
      } catch {
        return false;
      }
      try {
        await api('POST', '/auth/logout');
      } catch {
        notify('Could not reach the server to log out. Try again.');
        return false;
      }
      await client.cancelQueries();
      return true;
    }))
  )
    return;
  workspaceMenu.value = false;
  workspaceId.value = '';
  await nextTick();
  await router.replace('/signin');
  // Retain the shell's observers while dropping data from the previous session.
  await client.resetQueries();
}

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
      sidebarFooter.value?.focusWorkspace();
    } else {
      help.value = false;
      sidebarFooter.value?.focusHelp();
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
function go(id: string) {
  void router.push('/' + id);
  workspaceMenu.value = false;
  help.value = false;
}
function key(event: KeyboardEvent) {
  if (event.defaultPrevented || route.meta.bare) return;
  if (labWindow.value && !document.querySelector('dialog[open]')) {
    const shortcut = event.key.toLowerCase();
    if ((event.metaKey || event.ctrlKey) && !event.shiftKey && shortcut === 'j') {
      event.preventDefault();
      labWindow.value.toggle();
      return;
    }
    if (event.ctrlKey && event.shiftKey && shortcut === 'm') {
      event.preventDefault();
      labWindow.value.modelMenu();
      return;
    }
  }
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
    if (!paletteOpen.value && document.querySelector('dialog[open]')) return;
    event.preventDefault();
    if (paletteOpen.value) paletteOpen.value = false;
    else openSearch();
    return;
  }
  const editable = (event.target as HTMLElement).closest(
    'input, textarea, select, [contenteditable="true"]',
  );
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
watch(heading, (x) => (document.title = `${x} | Lykeion`), { immediate: true });
onMounted(() => {
  window.addEventListener('keydown', key);
  window.addEventListener('pointerdown', outsidePopup);
});
onUnmounted(() => {
  window.removeEventListener('keydown', key);
  window.removeEventListener('pointerdown', outsidePopup);
});
</script>
<template>
  <DiscardDraftDialog />
  <RouterView v-if="route.meta.bare" />
  <div
    v-else-if="!unauthorized && !onboardingTarget"
    class="application"
    :class="{
      'sidebar-hidden': collapsed,
      'sidebar-resizing': rail.dragging.value,
      'aristotle-shell': aristotleShell || inboxShell,
      desktop,
      'study-detail-open': Boolean(cardId),
    }"
    :style="{ '--rail-width': rail.width.value + 'px' }"
  >
    <WorkspaceTabs :title="tabTitle" :icon="headingIcon" />
    <!-- Outside the rails, so it stays reachable while the sidebar is hidden. -->
    <button
      class="icon-button sidebar-toggle"
      aria-label="Toggle left sidebar"
      :aria-expanded="!collapsed"
      @click="collapsed = !collapsed"
    >
      <Icon name="sidebar" :size="15" />
    </button>
    <!-- Kept mounted while the navigation is shown: the session teleports
         its sidebar into the slot and would lose it otherwise. -->
    <div
      v-if="cowriteShell"
      v-show="railView === 'context'"
      class="sidebar coresearch cowrite-context-rail"
      :inert="collapsed || undefined"
      :aria-hidden="collapsed || undefined"
    >
      <SidebarBrand
        :current="current"
        @reselect="changeRail(railView === 'context' ? 'nav' : 'context')"
      />
      <div id="cowrite-context-rail" class="cowrite-context-slot" />
      <SidebarFooter
        v-if="railView === 'context'"
        ref="sidebarFooter"
        :workspace-name="workspaceName"
        :selected="current === 'settings' || current === 'lab-chart'"
        :menu-open="workspaceMenu"
        :help-open="help"
        @toggle-workspace="toggleWorkspace"
        @toggle-help="toggleHelp"
      />
    </div>
    <ConversationRail v-else-if="aristotleShell && railView === 'context'" @search="openSearch">
      <template #brand>
        <SidebarBrand
          :current="current"
          @reselect="changeRail(railView === 'context' ? 'nav' : 'context')"
        />
      </template>
      <template #footer>
        <SidebarFooter
          ref="sidebarFooter"
          :workspace-name="workspaceName"
          :selected="current === 'settings' || current === 'lab-chart'"
          :menu-open="workspaceMenu"
          :help-open="help"
          @toggle-workspace="toggleWorkspace"
          @toggle-help="toggleHelp"
        />
      </template>
    </ConversationRail>
    <DecisionRail v-if="inboxShell" v-show="railView === 'context'" @search="openSearch">
      <template #brand>
        <SidebarBrand
          :current="current"
          @reselect="changeRail(railView === 'context' ? 'nav' : 'context')"
        />
      </template>
      <template #footer>
        <SidebarFooter
          ref="sidebarFooter"
          :workspace-name="workspaceName"
          :selected="current === 'settings' || current === 'lab-chart'"
          :menu-open="workspaceMenu"
          :help-open="help"
          @toggle-workspace="toggleWorkspace"
          @toggle-help="toggleHelp"
        />
      </template>
    </DecisionRail>
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
        <span class="profile-menu-avatar">{{
          (me.data.value?.email || '?')[0]?.toUpperCase()
        }}</span
        ><span>{{ me.data.value?.email }}</span>
      </button>
      <button role="menuitem" @click="go('settings')">
        <Icon name="settings" :size="16" />Settings
      </button>
      <button role="menuitem" @click="go('lab-chart')">
        <Icon name="network" :size="16" />Lykeion
      </button>
      <div class="shell-menu-divider" />
      <small class="workspace-menu-label">Workspaces</small>
      <button
        v-for="w in workspaces.data.value ?? []"
        :key="w.id"
        role="menuitem"
        @click="chooseWorkspace(w.id)"
      >
        <span class="workspace-avatar">{{ w.name[0] }}</span
        >{{ w.name }}<Icon v-if="w.id === workspaceId" name="check" class="push-right" :size="14" />
      </button>
      <button role="menuitem" @click="go('onboarding?step=workspace')">
        <Icon name="plus" :size="16" />Create workspace
      </button>
      <div class="shell-menu-divider" />
      <button role="menuitem" class="danger-menu-item" @click="logOut">
        <Icon name="logout" :size="16" />Log out
      </button>
    </div>
    <aside
      v-if="(!aristotleShell && !cowriteShell && !inboxShell) || railView === 'nav'"
      class="sidebar"
      :inert="collapsed || undefined"
      :aria-hidden="collapsed || undefined"
      aria-label="Workspace navigation"
    >
      <SidebarBrand
        :current="current"
        @reselect="changeRail(railView === 'context' ? 'nav' : 'context')"
      />
      <div class="quick-actions">
        <button
          class="nav-item palette-trigger"
          aria-label="Search"
          aria-keyshortcuts="Meta+K Control+K"
          @click="openSearch"
        >
          <Icon name="search" />Search…<kbd class="push-right">⌘K</kbd>
        </button>
        <button class="nav-item" aria-label="New" aria-keyshortcuts="C" @click="openCreate()">
          <Icon name="edit" />New<kbd class="push-right">C</kbd>
        </button>
      </div>
      <nav>
        <template v-for="(group, groupIndex) in groups" :key="group.name"
          ><section
            v-if="groupIndex === 1 && pinnedCards.length"
            class="nav-group pinned-study-group"
          >
            <button
              class="nav-caption pinned-caption"
              :aria-label="`Pinned ${pinnedCards.length}`"
              :aria-expanded="pinnedExpanded"
              @click="pinnedExpanded = !pinnedExpanded"
            >
              Pinned<Icon :name="pinnedExpanded ? 'down' : 'right'" :size="11" /></button
            ><template v-if="pinnedExpanded"
              ><RouterLink
                v-for="card in pinnedCards"
                :key="card.id"
                :to="'/studies/' + card.id"
                class="nav-item"
                :class="{ selected: cardId === card.id }"
                ><Icon
                  :name="card.ending ? 'check' : 'circle'"
                  :class="card.flag ? 'pinned-blocked' : ''"
                  :size="14"
                /><span>{{ studyDisplayTitle(card.title) }}</span></RouterLink
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
      <SidebarFooter
        ref="sidebarFooter"
        :workspace-name="workspaceName"
        :selected="current === 'settings' || current === 'lab-chart'"
        :menu-open="workspaceMenu"
        :help-open="help"
        @toggle-workspace="toggleWorkspace"
        @toggle-help="toggleHelp"
      />
    </aside>
    <button
      class="sidebar-handle"
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize left sidebar"
      :aria-valuenow="rail.width.value"
      :aria-valuemin="219"
      :aria-valuemax="rail.maximum.value"
      @pointerdown="rail.pointerdown($event, () => (collapsed = !collapsed))"
      @keydown="rail.keydown"
      @click="$event.detail === 0 && (collapsed = !collapsed)"
    />
    <main class="main-pane">
      <header
        v-if="current !== 'settings' && !route.meta.ownHeader && !shownReport"
        class="page-header"
        :class="{
          'machine-detail-shell-heading': current === 'machines' && route.params.machineId,
        }"
      >
        <div v-if="current === 'workflows'" class="workflow-heading">
          <h1>{{ heading }}</h1>
          <span class="workflow-attribution"
            >Workflows are ported from Terminal-Bench-Science.</span
          >
        </div>
        <h1 v-else>{{ heading }}</h1>
        <span id="page-header-count" class="page-header-count" />
        <div id="page-header-actions" class="page-header-actions" />
      </header>
      <small v-if="reconnecting" class="reconnecting shared-reconnecting" role="status"
        >Reconnecting…</small
      >
      <div v-if="shellFailure" class="center-empty" role="alert">
        <Icon name="link" :size="32" />
        <p>{{ shellFailure.message }}</p>
        <button class="button" :disabled="shellRetrying" @click="retryShell">Try again</button>
      </div>
      <div v-else-if="shellPending" class="center-empty" role="status">
        <Icon name="refresh" :size="28" /><span>Loading workspace…</span>
      </div>
      <template v-else>
        <RouterView />
        <RouterView v-if="cardId" name="detail" @close="go(current ?? 'studies')" />
      </template>
    </main>
    <AristotleWindow
      v-if="workspaceId && me.data.value?.id && !unauthorized && route.path !== '/decisions/new'"
      ref="labWindow"
      :key="workspaceId + ':' + me.data.value.id"
      :user-id="me.data.value.id"
      :study-id="cardId || shownReport?.id"
      :report-context="!cardId && !!shownReport"
      :study-title="shownCard ? studyDisplayTitle(shownCard.title) : shownReport?.title"
    />
    <CreateStudy
      v-if="composer.open"
      :assignee="composer.assignee"
      :project-id="composer.projectId"
      :workflow="composer.workflow"
      @close="closeComposer()"
    />
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
    <template v-if="paletteOpen">
      <ConversationSearch v-if="paletteScope === 'conversations'" @close="paletteOpen = false" />
      <DecisionSearch v-else-if="paletteScope === 'decisions'" @close="paletteOpen = false" />
      <CommandPalette v-else @close="paletteOpen = false" />
    </template>
    <Modal v-if="feedback" title="Feedback" @close="feedback = false"
      ><form class="feedback-form" @submit.prevent="copyFeedback">
        <header>
          <h2>Feedback</h2>
          <button
            type="button"
            class="icon-button"
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
  bottom: 48px;
  width: 224px;
  max-height: calc(100dvh - 60px);
  overflow-y: auto;
}
.workspace-popup > .workspace-profile {
  height: 44px;
  padding: 4px 6px;
  gap: 12px;
}
.workspace-profile > span:last-child {
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
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
  font-size: 13px;
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
  color: var(--primary-text);
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
  font-size: 13px;
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
  resize: none;
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
  .study-detail-open .aristotle-launcher {
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
.pinned-study-group .nav-item > span {
  white-space: nowrap;
  text-overflow: ellipsis;
  overflow: hidden;
}
.pinned-blocked {
  color: #dc354d;
}
.reconnecting {
  margin-left: auto;
  color: var(--muted);
  font-size: 13px;
}
.shared-reconnecting {
  flex: none;
  align-self: flex-end;
  padding: 4px 16px;
}
</style>

<style scoped>
.workflow-heading {
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.workflow-attribution {
  color: var(--muted);
  font-size: 13px;
}
.page-header-count {
  margin-left: 8px;
  color: var(--muted);
  font-size: 13px;
}
.page-header-actions {
  display: flex;
  align-items: center;
  margin-left: auto;
}
.application .sidebar {
  flex-basis: var(--rail-width);
  width: var(--rail-width);
  overflow: hidden;
  transition:
    flex-basis 220ms cubic-bezier(0.22, 1, 0.36, 1),
    width 220ms cubic-bezier(0.22, 1, 0.36, 1),
    padding 220ms cubic-bezier(0.22, 1, 0.36, 1);
}
.application.sidebar-hidden .sidebar {
  display: flex;
  flex-basis: 0;
  width: 0;
  padding-inline: 0;
}
.application.sidebar-resizing .sidebar {
  transition: none;
}
@media (prefers-reduced-motion: reduce) {
  .application .sidebar {
    transition: none;
  }
}
.application .sidebar-handle {
  left: calc(var(--rail-width) - 5px);
}
/* Sits in the brand row's left gap, left of the wordmark. */
.sidebar-toggle {
  position: fixed;
  top: 60px;
  left: 10px;
  z-index: 40;
  width: 28px;
  height: 32px;
}
.application.sidebar-hidden .sidebar-handle {
  left: 0;
}
.sidebar-handle:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: -2px;
}
</style>

<style scoped>
.machine-detail-shell-heading {
  position: absolute;
  z-index: 4;
  height: 0;
  padding: 0;
  border: 0;
}
.machine-detail-shell-heading h1 {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
.sidebar-hidden :deep(.machine-breadcrumb) {
  padding-left: 32px;
}
.machine-detail-shell-heading .icon-button {
  position: absolute;
  top: 12px;
  left: 12px;
}
</style>

<style>
@media (max-width: 760px) {
  .application.aristotle-shell:not(.sidebar-hidden) .sidebar {
    display: flex;
    flex-direction: column;
    width: 200px;
    flex-basis: 200px;
  }
  .application.aristotle-shell:not(.sidebar-hidden) .main-pane {
    min-width: 0;
  }
}
</style>

<style>
@media (max-width: 700px) {
  .application.aristotle-shell {
    --aristotle-mobile-dock-height: 64px;
    padding-bottom: var(--aristotle-mobile-dock-height);
  }
  .application.aristotle-shell:not(.sidebar-hidden) .sidebar {
    position: fixed;
    left: 0;
    top: 62px;
    bottom: var(--aristotle-mobile-dock-height);
    width: 262px;
    z-index: 25;
    background: var(--canvas);
    border-right: 1px solid var(--border);
  }
  .application.aristotle-shell .main-pane {
    width: 100%;
  }
}
</style>

<style>
.application .sidebar.cowrite-context-rail {
  padding: 0;
  overflow: hidden;
  background: var(--sidebar);
}
.application .cowrite-context-rail .task-sidebar {
  padding-top: 0;
}
.application .sidebar.cowrite-context-rail .task-sidebar :is(div, button, span, p, strong, small) {
  color: #666a6d;
}
.application .cowrite-context-rail > .sidebar-brand {
  margin: 0 10px 12px;
}
.application .cowrite-context-rail > .sidebar-bottom {
  padding: 0 10px;
  flex: none;
}
.application .cowrite-context-slot {
  display: flex;
  flex: 1;
  min-height: 0;
}
.application .cowrite-context-slot > * {
  flex: 1;
  min-width: 0;
}
/* With the sidebar hidden the toggle sits over the page's top-left corner;
   each page's header starts just past it while the page keeps its full width. */
.application.sidebar-hidden
  :is(
    .main-pane > .page-header,
    .study-heading,
    .aristotle-header,
    .coresearch-list-header,
    .agent-create-header
  ) {
  padding-left: 44px;
}
.application.sidebar-hidden .settings-rail > h1 {
  padding-left: 34px;
}
</style>
