<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { state, service, refresh, notify } from '../services/workspace';
import Icon from '../components/Icon.vue';
import AvatarDisplay from '../components/AvatarDisplay.vue';
import { GitBranch } from '@lucide/vue';
import Modal from '../components/Modal.vue';
import ResourceEditor from '../components/ResourceEditor.vue';
import AgentConfiguration from '../components/AgentConfiguration.vue';
import ResourceDetail from '../components/ResourceDetail.vue';
import TaskWakeups from '../components/TaskWakeups.vue';
const props = defineProps<{ page: string }>();
const emit = defineEmits<{ createTask: []; assignAgent: [string] }>();
const router = useRouter(),
  route = useRoute();
const wakeups = ref<InstanceType<typeof TaskWakeups>>();
const search = ref(''),
  tab = ref('Mine'),
  autopilotTab = ref('Autopilot'),
  menu = ref(''),
  dialog = ref(''),
  selected = ref('');
const projectExpanded = ref(false);
const name = ref(''),
  description = ref(''),
  instructions = ref(''),
  schedule = ref('Every weekday at 09:00'),
  endpoint = ref(''),
  runtime = ref(''),
  error = ref(''),
  busy = ref(false);
const newChat = ref(Boolean(route.query.agent)),
  chatText = ref(''),
  archived = ref(false),
  unreadOnly = ref(false);
const notifications = computed(
  () => state.data?.notifications.filter((x) => !unreadOnly.value || !x.read) ?? [],
);
const sort = ref(false),
  statusFilter = ref('all'),
  expanded = ref(true),
  automaticUpdates = ref(localStorage.getItem('lykeion.automatic-updates') !== 'false');
const agentCreation = computed(() => props.page === 'agents' && route.path.includes('/new'));
const agentManual = computed(() => route.path.endsWith('/manual'));
const machines = computed(() => state.data?.machines ?? []);
const detailId = computed(() =>
  ['agents', 'runtimes'].includes(props.page) &&
  route.path.split('/')[2] &&
  route.path.split('/')[2] !== 'new'
    ? route.path.split('/')[2]
    : null,
);
const agents = computed(() =>
  [...(state.data?.agents ?? [])]
    .filter(
      (x) =>
        x.name.toLowerCase().includes(search.value.toLowerCase()) &&
        (statusFilter.value === 'all' || x.status === statusFilter.value),
    )
    .sort((a, b) => (sort.value ? a.name.localeCompare(b.name) : 0)),
);
const templates = [
  {
    name: 'Literature watch',
    icon: 'book',
    text: 'Track new papers, datasets, and evidence for your research',
  },
  {
    name: 'Hypothesis review',
    icon: 'sparkles',
    text: 'Check predictions, falsifiability, and competing explanations',
  },
  {
    name: 'Experiment monitor',
    icon: 'flask',
    text: 'Review experiment results, failures, and next steps',
  },
  {
    name: 'Weekly research report',
    icon: 'chart',
    text: 'Summarize evidence, experiments, and research…',
  },
  {
    name: 'Reproducibility audit',
    icon: 'shield',
    text: 'Check methods, data provenance, and experiment…',
  },
  {
    name: 'Manuscript evidence check',
    icon: 'file',
    text: 'Verify claims, citations, and figures against the evidence',
  },
];
function open(kind: string, preset = '') {
  dialog.value = kind;
  name.value = preset;
  description.value = '';
  instructions.value = '';
  endpoint.value = '';
  error.value = '';
  menu.value = '';
}
async function saveProject() {
  busy.value = true;
  error.value = '';
  try {
    await service.value.createProject({ name: name.value, description: description.value });
    await refresh();
    dialog.value = '';
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function saveAgent() {
  busy.value = true;
  error.value = '';
  try {
    await service.value.createResource('agents', {
      name: name.value,
      role: instructions.value,
      runtimeId: runtime.value || null,
    });
    await refresh();
    await router.push('/agents');
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
const libraryItems = computed(() =>
  props.page === 'skills'
    ? (state.data?.skills ?? []).filter((x) =>
        x.name.toLowerCase().includes(search.value.toLowerCase()),
      )
    : (state.data?.connectors ?? []).filter((x) =>
        x.name.toLowerCase().includes(search.value.toLowerCase()),
      ),
);
function unavailable(feature: string) {
  notify(`${feature} requires a connected Lykeion control plane.`);
}
watch(
  () => props.page,
  () => {
    search.value = '';
    menu.value = '';
    selected.value = '';
    dialog.value = '';
  },
);
watch(
  () => route.query.agent,
  (value) => {
    if (value) newChat.value = true;
  },
);
const chatAgent = computed(() => state.data?.agents.find((x) => x.id === route.query.agent));
const chatStarters = computed(() =>
  chatAgent.value?.conversationStarters?.length
    ? chatAgent.value.conversationStarters
    : chatAgent.value?.suggestions?.length
      ? chatAgent.value.suggestions.map((prompt) => ({ label: prompt, prompt }))
      : [
          'Ground a research question',
          'Design a reproducible experiment',
          'Review evidence and findings',
        ].map((prompt) => ({ label: prompt, prompt })),
);
watch(automaticUpdates, (value) =>
  localStorage.setItem('lykeion.automatic-updates', String(value)),
);
</script>
<template>
  <section class="resources" :class="'resource-' + page">
    <div v-if="!agentCreation" class="resource-header-actions">
      <button v-if="page === 'projects'" class="button primary" @click="open('project')">
        <Icon name="plus" />New research
      </button>
      <button
        v-else-if="page === 'agents'"
        class="button primary"
        @click="router.push('/agents/new')"
      >
        <Icon name="plus" />New agent
      </button>
      <button v-else-if="page === 'squads'" class="button primary" @click="open('team')">
        <Icon name="plus" />New team
      </button>
      <button
        v-else-if="page === 'autopilots'"
        class="button primary"
        @click="autopilotTab === 'Task wakeups' ? wakeups?.open() : open('autopilot')"
      >
        <Icon name="plus" />{{ autopilotTab === 'Task wakeups' ? 'New wakeup' : 'New autopilot' }}
      </button>
      <template v-else-if="page === 'runtimes'">
        <button class="button" @click="open('daemon')"><Icon name="network" />Daemon</button>
        <button class="button primary" @click="open('machine')">
          <Icon name="plus" />Add a computer
        </button>
      </template>
      <button v-else-if="page === 'skills'" class="button primary" @click="open('skills')">
        <Icon name="plus" />Add skill<Icon name="down" :size="12" />
      </button>
      <button v-else-if="page === 'connectors'" class="button primary" @click="open('connector')">
        <Icon name="plus" />Add connector
      </button>
    </div>
    <template v-if="page === 'projects'">
      <div v-if="state.data?.projects.length" class="project-list">
        <button
          v-for="project in state.data.projects"
          :key="project.id"
          @click="selected = project.id"
        >
          <Icon name="folder" :size="22" />
          <span>
            <strong>{{ project.name }}</strong>
            <small>{{ project.description }}</small>
          </span>
          <span class="push-right"
            >{{ state.data.inquiries.filter((x) => x.projectId === project.id).length }} tasks</span
          >
          <Icon name="right" />
        </button>
      </div>
      <div v-else class="resource-center">
        <span class="round-icon">
          <Icon name="folder" />
        </span>
        <p>No projects yet</p>
        <button class="button primary compact" @click="open('project')">
          Create your first research
        </button>
      </div>
    </template>
    <template v-else-if="page === 'squads'">
      <div v-if="state.data?.teams.length" class="project-list">
        <button
          v-for="team in state.data.teams"
          :key="team.id"
          @click="notify(team.description || 'No description.')"
        >
          <Icon name="users" :size="24" />
          <span
            >{{ team.name }}<small>{{ team.description }}</small>
          </span>
          <small class="push-right">{{ team.agentIds.length }} members</small>
        </button>
      </div>
      <div v-else class="resource-center">
        <span class="round-icon">
          <Icon name="users" />
        </span>
        <p>No teams yet</p>
        <button class="button primary compact" @click="open('team')">
          <Icon name="plus" />New team
        </button>
      </div>
    </template>
    <template v-else-if="page === 'colleagues'">
      <p class="section-intro">Open a colleague to see their work in this workspace.</p>
      <div v-if="state.data?.colleagues.length" class="colleague-list">
        <button
          v-for="colleague in state.data.colleagues"
          :key="colleague.id"
          @click="notify(colleague.role + ' · ' + colleague.email)"
        >
          <span class="workspace-avatar">{{ colleague.name[0] }}</span>
          <span
            >{{ colleague.name }}<small>{{ colleague.email }}</small>
          </span>
        </button>
      </div>
      <div v-else class="small-empty">
        <Icon name="users" :size="22" />
        <p>No colleagues yet</p>
      </div>
    </template>
    <template v-else-if="page === 'autopilots'">
      <div class="resource-tabs">
        <button
          v-for="t in ['Autopilot', 'Task wakeups']"
          :class="{ active: autopilotTab === t }"
          @click="autopilotTab = t"
        >
          {{ t }}
        </button>
      </div>
      <div
        v-if="autopilotTab === 'Autopilot' && state.data?.autopilots.length"
        class="project-list"
      >
        <button
          v-for="autopilot in state.data.autopilots"
          :key="autopilot.id"
          @click="notify(autopilot.instructions)"
        >
          <Icon name="zap" />
          <span
            >{{ autopilot.name }}<small>{{ autopilot.schedule }}</small>
          </span>
          <small class="push-right">{{ autopilot.status }}</small>
        </button>
      </div>
      <div v-else-if="autopilotTab === 'Autopilot'" class="autopilot-content">
        <Icon name="zap" :size="40" />
        <p>No autopilots yet</p>
        <small
          >Schedule recurring work for your AI agents. Pick a template or start from scratch.</small
        >
        <div class="template-grid">
          <button v-for="item in templates" :key="item.name" @click="open('autopilot', item.name)">
            <Icon :name="item.icon" :size="20" />
            <span
              >{{ item.name }}<small>{{ item.text }}</small>
            </span>
          </button>
        </div>
        <button class="button primary compact" @click="open('autopilot')">
          <Icon name="plus" />Start from scratch
        </button>
      </div>
      <TaskWakeups v-else ref="wakeups" />
    </template>
    <template v-else-if="page === 'agents'">
      <template v-if="agentCreation">
        <div class="agent-heading">
          <button class="icon-button" aria-label="Back to agents" @click="router.push('/agents')">
            <Icon name="back" />
          </button>
          <span
            >Create an agent<small>{{
              agentManual ? 'Review and configure' : 'Choose a starting point'
            }}</small> </span
          ><span v-if="agentManual" class="creation-mode">Start blank</span>
        </div>
        <div v-if="!agentManual" class="agent-start">
          <small>AGENT CREATION</small>
          <h2>How would you like to start?</h2>
          <div class="agent-options">
            <button @click="router.push('/agents/new/manual')">
              <span class="square-icon">
                <Icon name="file" :size="22" />
              </span>
              <strong>Start blank</strong>
              <p>Set the name, instructions, and runtime yourself.</p>
              <small>Continue <Icon name="right" :size="12" /> </small>
            </button>
            <button class="recommended" @click="unavailable('Building an agent with AI')">
              <span class="recommend-label">Recommended</span>
              <span class="square-icon">
                <Icon name="chat" :size="22" />
              </span>
              <strong>Build with AI</strong>
              <p>Create an editable configuration through conversation.</p>
              <small>Continue <Icon name="right" :size="12" /> </small>
            </button>
          </div>
        </div>
        <AgentConfiguration v-else />
      </template>
      <template v-else>
        <div class="agent-toolbar">
          <label class="agent-search">
            <Icon name="search" :size="14" />
            <input v-model="search" placeholder="Search agents..." aria-label="Search agents" />
          </label>
          <button
            v-for="t in ['Mine', 'All', 'Archived']"
            class="button compact"
            :class="{ active: tab === t }"
            @click="tab = t"
          >
            {{ t }} <small>{{ t === 'Archived' ? 0 : agents.length }}</small>
          </button>
          <div class="push-right menu-anchor">
            <button class="button" @click="menu = menu ? '' : 'filter'">
              <Icon name="filter" :size="14" />Filter
            </button>
            <div v-if="menu === 'filter'" class="floating-menu">
              <button
                v-for="s in ['all', 'online', 'offline', 'busy']"
                @click="
                  statusFilter = s;
                  menu = '';
                "
              >
                {{ s }}<Icon v-if="statusFilter === s" class="push-right" name="check" />
              </button>
            </div>
          </div>
          <button class="button" @click="sort = !sort">
            <Icon name="down" :size="14" />{{ sort ? 'Name' : 'Last active' }}
          </button>
        </div>
        <div class="agent-table">
          <div class="agent-table-head">
            <span>Agent</span>
            <span>Status</span>
            <span>Owner</span>
            <span>Access</span>
            <span>Runtime</span>
            <span>Last active ↓</span>
            <span>Runs</span>
          </div>
          <div
            v-if="tab !== 'Archived'"
            v-for="agent in agents"
            :key="agent.id"
            class="agent-table-row"
            role="button"
            tabindex="0"
            @click="router.push('/agents/' + agent.id)"
            @keydown.enter.prevent="router.push('/agents/' + agent.id)"
            @keydown.space.prevent="router.push('/agents/' + agent.id)"
          >
            <span>
              <AvatarDisplay :value="agent.avatar" :size="28" />
              <span
                >{{ agent.name }}<small>{{ agent.role }}</small>
              </span>
            </span>
            <span>{{ agent.status }}</span>
            <span>—</span>
            <span>Owner only</span>
            <span>—</span>
            <span>—</span>
            <span>—</span>
          </div>
          <div v-if="!agents.length || tab === 'Archived'" class="small-empty">
            <Icon name="bot" :size="24" />
            <p>
              {{
                search
                  ? 'No matching agents'
                  : tab === 'Archived'
                    ? 'No archived agents'
                    : 'No agents yet'
              }}
            </p>
          </div>
        </div>
      </template>
    </template>
    <template v-else-if="page === 'skills' || page === 'connectors'">
      <div class="library-toolbar">
        <button class="library-filter" @click="search = ''">
          All ({{
            page === 'skills'
              ? (state.data?.skills.length ?? 0)
              : (state.data?.connectors.length ?? 0)
          }})
        </button>
        <label>
          <Icon name="search" :size="14" />
          <input
            v-model="search"
            :placeholder="`Search ${page}...`"
            :aria-label="`Search ${page}`"
          />
        </label>
      </div>
      <div class="library-section">
        <button class="library-heading" :aria-expanded="expanded" @click="expanded = !expanded">
          {{ page === 'skills' ? 'Workspace skills' : 'Shared connectors'
          }}<Icon name="down" :size="12" />
        </button>
        <p v-if="page === 'skills'">Instructions any agent in this workspace can use.</p>
        <p v-else class="connector-note">
          For security, saved URLs, commands, headers, and environment values aren't shown again. To
          change them, enter a complete new configuration. Renaming keeps the current configuration
          and agent assignments.
        </p>
        <div v-if="expanded && libraryItems.length" class="project-list">
          <button
            v-for="item in libraryItems"
            :key="item.id"
            @click="notify('description' in item ? item.description : item.status)"
          >
            <Icon :name="page === 'skills' ? 'book' : 'link'" />
            <span
              >{{ item.name
              }}<small>{{ 'description' in item ? item.description : item.provider }}</small>
            </span>
          </button>
        </div>
        <div v-else-if="expanded" class="small-empty">
          <Icon :name="page === 'skills' ? 'book' : 'link'" :size="20" />
          <p>
            {{
              search
                ? 'No matching ' + page
                : page === 'skills'
                  ? 'No skills yet'
                  : 'No shared connectors'
            }}
          </p>
          <small v-if="page === 'skills' && !search">Create or import a skill.</small>
        </div>
      </div>
    </template>
    <template v-else-if="page === 'runtimes'">
      <div class="inset-content">
        <div v-if="machines.length" class="machine-list">
          <button v-for="r in machines" :key="r.id" @click="router.push('/runtimes/' + r.id)">
            <span class="square-icon">
              <Icon name="monitor" />
            </span>
            <span
              >{{ r.name }}<small>{{ r.platform }}</small>
            </span>
            <span class="push-right">{{ r.status }}</span>
            <Icon name="right" />
          </button>
        </div>
        <div v-else class="small-empty">
          <Icon name="monitor" :size="28" />
          <p>No computers connected</p>
          <small>Add a computer to run your agents.</small>
          <button class="button primary" @click="open('machine')">
            <Icon name="plus" />Add a computer
          </button>
        </div>
      </div>
    </template>
    <template v-else-if="page === 'updates'">
      <div class="inset-content">
        <div class="updates-card">
          <div>
            <span>Current version</span>
            <small class="push-right">0.1.0</small>
          </div>
          <div>
            <span
              >Automatic background updates<small
                >Preference saved for when an update provider is connected. Background updates are
                currently unavailable.</small
              >
            </span>
            <button
              class="update-switch push-right"
              :class="{ on: automaticUpdates }"
              role="switch"
              :aria-checked="automaticUpdates"
              aria-label="Automatic background updates"
              @click="automaticUpdates = !automaticUpdates"
            >
              <span />
            </button>
          </div>
          <div>
            <span
              >Check for updates<small
                >You'll be prompted to restart when the download is ready.</small
              >
            </span>
            <button
              class="button compact push-right"
              @click="
                notify('An update provider has not been configured for this independent build.')
              "
            >
              Check now
            </button>
          </div>
        </div>
      </div>
    </template>
    <template v-else-if="page === 'inbox' || page === 'chat'">
      <div class="split-workspace">
        <aside class="conversation-list">
          <header>
            <h2>{{ page === 'inbox' ? 'Inbox' : 'Chat' }}</h2>
            <button
              v-if="page === 'chat'"
              class="icon-button push-right"
              aria-label="New chat"
              @click="newChat = true"
            >
              <Icon name="plus" />
            </button>
            <template v-else>
              <button
                class="icon-button push-right"
                aria-label="Filter notifications"
                @click="menu = menu ? '' : 'notifications'"
              >
                <Icon name="filter" />
              </button>
              <button
                class="icon-button"
                aria-label="Inbox options"
                @click="notify('Notification actions become available when notifications arrive.')"
              >
                <Icon name="more" />
              </button>
            </template>
          </header>
          <p v-if="page === 'chat' && !state.data?.conversations.length">No previous tasks</p>
          <button
            v-if="page === 'chat'"
            v-for="conversation in state.data?.conversations"
            class="conversation-row"
            @click="
              newChat = true;
              notify('Conversation messages require the chat adapter.');
            "
          >
            {{ conversation.title }}
          </button>
          <template v-else>
            <div v-if="menu === 'notifications'" class="notification-filter">
              <button
                @click="
                  archived = false;
                  unreadOnly = false;
                  menu = '';
                "
              >
                All notifications
              </button>
              <button
                @click="
                  archived = false;
                  unreadOnly = true;
                  menu = '';
                "
              >
                Unread
              </button>
            </div>
            <button
              v-for="notification in notifications"
              v-show="!archived"
              class="conversation-row"
              @click="notify(notification.body)"
            >
              <Icon name="bell" />
              <span
                >{{ notification.title }}<small>{{ notification.body }}</small>
              </span>
            </button>
            <p v-if="!notifications.length && !archived">No notifications yet</p>
            <button class="archive-row" @click="archived = !archived">
              <Icon name="archive" />
              <span>Archived</span>
              <Icon name="right" :size="12" class="push-right" />
            </button>
            <p v-if="archived">No archived notifications</p>
          </template>
        </aside>
        <div class="conversation-content">
          <template v-if="page === 'chat' && newChat">
            <div class="chat-start">
              <span class="round-icon">
                <Icon name="bot" :size="24" />
              </span>
              <h2>
                {{
                  chatAgent
                    ? 'Chat with ' + chatAgent.name
                    : state.data?.agents.length
                      ? 'Start a task with an agent'
                      : 'Connect an agent to start'
                }}
              </h2>
              <p>
                Explore the literature, develop testable hypotheses, and<br />
                design reproducible experiments.
              </p>
              <small>Ask for a summary, plan your day, or hand off a quick task.</small>
              <button
                v-for="starter in chatStarters"
                :key="starter.label"
                @click="chatText = starter.prompt"
              >
                {{ starter.label }}
              </button>
            </div>
            <form class="chat-composer" @submit.prevent="unavailable('Agent conversations')">
              <textarea v-model="chatText" placeholder="Start a task..." aria-label="Message" />
              <footer>
                <button
                  type="button"
                  class="icon-button"
                  aria-label="Attach file"
                  @click="unavailable('Chat attachments')"
                >
                  <Icon name="plus" />
                </button>
                <span>Experiment</span>
                <span>Cowrite</span>
                <button
                  type="submit"
                  class="icon-button push-right"
                  aria-label="Send message"
                  :disabled="!chatText.trim()"
                >
                  <Icon name="up" />
                </button>
              </footer>
            </form>
          </template>
          <div v-else class="conversation-empty">
            <Icon :name="page === 'inbox' ? 'inbox' : 'chat'" :size="38" />
            <p>
              {{
                page === 'inbox'
                  ? 'Select a notification to view details'
                  : 'Pick a task, or start a new one with +'
              }}
            </p>
          </div>
        </div>
      </div>
    </template>
    <ResourceDetail
      v-if="detailId"
      :kind="page === 'agents' ? 'agents' : 'machines'"
      :id="detailId"
      @assign="emit('assignAgent', $event)"
    />
  </section>
  <Modal
    v-if="dialog === 'project'"
    :class="{ 'expanded-project': projectExpanded }"
    title="New project"
    @close="dialog = ''"
  >
    <form class="research-create" @submit.prevent="saveProject">
      <header>
        <span>{{ state.data?.workspace.name }}</span>
        <Icon name="right" :size="12" />
        <strong>New project</strong>
        <button
          type="button"
          class="icon-button push-right"
          :aria-label="projectExpanded ? 'Collapse project editor' : 'Expand project editor'"
          :aria-pressed="projectExpanded"
          @click="projectExpanded = !projectExpanded"
        >
          <Icon name="expand" />
        </button>
        <button type="button" class="icon-button" aria-label="Close" @click="dialog = ''">
          <Icon name="close" />
        </button>
      </header>
      <div class="research-create-body">
        <span class="project-folder" aria-hidden="true">📁</span>
        <input
          v-model="name"
          placeholder="Project title"
          aria-label="Project title"
          required
          autofocus
        />
        <textarea
          v-model="description"
          placeholder="Add description..."
          aria-label="Project description"
        />
      </div>
      <div class="research-properties">
        <button type="button" class="pill" @click="notify('New researches start in Planned.')">
          ● Planned
        </button>
        <button
          type="button"
          class="pill"
          @click="notify('Priority can be assigned after creation.')"
        >
          — No priority
        </button>
        <button type="button" class="pill" @click="unavailable('Assigning a project lead')">
          Lead
        </button>
        <button type="button" class="pill" @click="unavailable('Repository connections')">
          <GitBranch :size="12" :stroke-width="1.7" />Repos
        </button>
        <button
          type="button"
          class="pill"
          @click="unavailable('Additional project properties')"
          aria-label="More project properties"
        >
          <Icon name="more" :size="14" />
        </button>
      </div>
      <p v-if="error" class="form-error" role="alert">{{ error }}</p>
      <footer>
        <button class="button primary compact push-right" :disabled="busy || !name.trim()">
          {{ busy ? 'Creating…' : 'Create Project' }}
        </button>
      </footer>
    </form>
  </Modal>
  <ResourceEditor v-else-if="dialog" :kind="dialog" :preset="name" @close="dialog = ''" />
  <Modal v-if="selected" title="Research project" @close="selected = ''">
    <header class="resource-modal-heading">
      <h2>{{ state.data?.projects.find((x) => x.id === selected)?.name }}</h2>
      <button class="icon-button" aria-label="Close project" @click="selected = ''">
        <Icon name="close" />
      </button>
    </header>
    <div class="resource-form">
      <p>
        {{ state.data?.projects.find((x) => x.id === selected)?.description || 'No description.' }}
      </p>
      <h3>Tasks</h3>
      <button
        v-for="task in state.data?.inquiries.filter((x) => x.projectId === selected)"
        class="nav-item"
        @click="router.push('/tasks/' + task.id)"
      >
        {{ task.title }}
      </button>
      <p v-if="!state.data?.inquiries.some((x) => x.projectId === selected)" class="muted">
        No tasks yet
      </p>
    </div>
  </Modal>
</template>
<style scoped src="./resources.css"></style>
