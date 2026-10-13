<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { state, service, refresh, notify } from '../services/workspace';
import AvatarDisplay from './AvatarDisplay.vue';
import Icon from './Icon.vue';
import Modal from './Modal.vue';
import AgentPanels from './AgentPanels.vue';
import AgentWork from './AgentWork.vue';
import RuntimeDetail from './RuntimeDetail.vue';
import CustomRuntime from './CustomRuntime.vue';
const props = defineProps<{ kind: 'agents' | 'machines'; id: string; runtimeId?: string }>();
const emit = defineEmits<{ assign: [string] }>();
const router = useRouter();
const customRuntime = ref(false),
  runtimeMenu = ref('');
const selectedRuntimeId = computed(() => props.runtimeId ?? null);
function openRuntimeTab(id: string, title: string) {
  window.dispatchEvent(
    new CustomEvent('lykeion:open-tab', {
      detail: { path: '/runtimes/' + props.id + '/runtime/' + id, title, icon: 'network' },
    }),
  );
  runtimeMenu.value = '';
}
const tab = ref('Overview'),
  menu = ref(false),
  rename = ref(false),
  newName = ref(''),
  error = ref(''),
  busy = ref(false),
  selectedSkill = ref('');
const agent = computed(() => state.data?.agents.find((x) => x.id === props.id));
const machine = computed(() => state.data?.machines.find((x) => x.id === props.id));
const record = computed(() => (props.kind === 'agents' ? agent.value : machine.value));
const runtime = computed(() => state.data?.runtimes.find((x) => x.id === agent.value?.runtimeId));
const runtimes = computed(() => state.data?.runtimes.filter((x) => x.machineId === props.id) ?? []);
const owner = computed(() => state.data?.colleagues.find((x) => x.id === agent.value?.ownerId));
const skills = computed(
  () => state.data?.skills.filter((x) => agent.value?.skillIds?.includes(x.id)) ?? [],
);
const tasks = computed(() => state.data?.inquiries.filter((x) => x.assigneeId === props.id) ?? []);
const access = computed(() =>
  agent.value?.access === 'workspace'
    ? 'Workspace'
    : agent.value?.access === 'specific'
      ? 'Specific people'
      : 'Personal',
);
function unavailable(action: string) {
  notify(`${action} requires a connected execution adapter. No command was sent.`);
}
async function saveName() {
  busy.value = true;
  error.value = '';
  try {
    await service.value.updateResource('machines', props.id, { name: newName.value });
    await refresh();
    rename.value = false;
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
watch(
  () => props.id,
  () => {
    tab.value = 'Overview';
    menu.value = false;
  },
);
</script>
<template>
  <section class="resource-detail">
    <div v-if="!record" class="missing-resource">
      <Icon name="folder" :size="30" />
      <h2>{{ kind === 'agents' ? 'Agent' : 'Machine' }} not found</h2>
      <button class="button" @click="router.push(kind === 'agents' ? '/agents' : '/runtimes')">
        Back to {{ kind === 'agents' ? 'agents' : 'machines' }}
      </button>
    </div>
    <template v-else>
      <header class="detail-top">
        <div class="detail-rail">
          <div class="detail-crumb">
            <button @click="router.push(kind === 'agents' ? '/agents' : '/runtimes')">
              {{ kind === 'agents' ? 'Agents' : 'Machines' }}
            </button>
            <span>/</span>{{ record.name }}
          </div>
          <div class="resource-identity">
            <span class="resource-avatar" :class="{ machine: kind === 'machines' }">
              <AvatarDisplay v-if="kind === 'agents'" :value="agent?.avatar" :size="36" /><Icon
                v-else
                name="monitor"
                :size="28"
              />
            </span>
            <div class="identity-copy">
              <div class="identity-title">
                <h1>{{ record.name }}</h1>
                <span
                  class="record-status"
                  :class="{ online: record.status === 'online' || record.status === 'connected' }"
                  >● {{ record.status }}</span
                >
              </div>
              <p>
                {{
                  kind === 'agents'
                    ? (agent?.description ?? agent?.role ?? 'No description.')
                    : (machine?.platform ?? '')
                }}
              </p>
              <div v-if="kind === 'agents'" class="identity-meta">
                <span> <Icon name="bot" :size="14" />{{ agent?.model || 'Runtime default' }}</span>
                <span> <Icon name="network" :size="14" />{{ runtime?.name ?? 'No runtime' }}</span>
                <span> <Icon name="shield" :size="14" />{{ access }}</span>
              </div>
              <div v-else class="identity-meta">
                <span>{{ runtimes.length }} runtimes</span>
                <span>Activity —</span>
                <span>CLI version —</span>
              </div>
            </div>
            <div class="detail-actions">
              <template v-if="kind === 'agents'">
                <button class="button compact" @click="router.push('/chat?agent=' + id)">
                  <Icon name="chat" :size="14" />DM
                </button>
                <button class="button primary compact" @click="emit('assign', id)">
                  <Icon name="plus" :size="14" />Assign work
                </button>
                <div class="menu-anchor">
                  <button class="icon-button" aria-label="Agent actions" @click="menu = !menu">
                    <Icon name="more" />
                  </button>
                  <div v-if="menu" class="floating-menu">
                    <button
                      @click="
                        unavailable('Archiving an agent');
                        menu = false;
                      "
                    >
                      <Icon name="archive" />Archive agent
                    </button>
                  </div>
                </div>
              </template>
              <template v-else>
                <button
                  class="button compact"
                  @click="
                    newName = record.name;
                    rename = true;
                  "
                >
                  <Icon name="edit" :size="14" />Rename machine
                </button>
                <button
                  class="button compact borderless"
                  @click="unavailable('Viewing machine logs')"
                >
                  <Icon name="file" :size="14" />View logs
                </button>
                <button class="button compact" @click="unavailable('Restarting a machine')">
                  <Icon name="refresh" :size="14" />Restart
                </button>
                <button class="button compact stop" @click="unavailable('Stopping a machine')">
                  <Icon name="circle" :size="14" />Stop
                </button>
              </template>
            </div>
          </div>
        </div>
      </header>
      <template v-if="kind === 'agents' && agent">
        <nav class="detail-tabs" aria-label="Agent page">
          <div class="detail-rail">
            <button
              v-for="name in ['Overview', 'Work', 'Capabilities', 'Settings']"
              :key="name"
              role="tab"
              :aria-selected="tab === name"
              :class="{ selected: tab === name }"
              @click="tab = name"
            >
              {{ name }}
            </button>
          </div>
        </nav>
        <AgentPanels
          v-if="tab === 'Settings' || tab === 'Capabilities'"
          :key="agent.id"
          :agent="agent"
          :tab="tab"
        /><AgentWork v-else-if="tab === 'Work'" :agent-id="agent.id" />
        <div v-else class="detail-body detail-rail">
          <main>
            <template v-if="tab === 'Overview'">
              <section class="current-work">
                <h2>
                  Now
                  <small>{{ agent.status === 'busy' ? 'Busy' : 'No active work reported' }}</small>
                </h2>
                <p>
                  {{
                    agent.status === 'busy'
                      ? 'This agent is busy. Run details will appear when supplied by the execution adapter.'
                      : 'No run activity is available for this agent.'
                  }}
                </p>
              </section>
              <section class="recent-work">
                <h2>Recent work</h2>
                <div class="work-empty">No run history available.</div>
              </section>
            </template>
            <template v-else-if="tab === 'Work'">
              <h2>
                Assigned tasks <small>{{ tasks.length }}</small>
              </h2>
              <div v-if="!tasks.length" class="work-empty">No tasks assigned.</div>
              <button
                v-for="task in tasks"
                :key="task.id"
                class="work-row"
                @click="router.push('/tasks/' + task.id)"
              >
                <Icon name="circle" />
                <span
                  >{{ task.title }}<small>{{ task.stage }} · {{ task.status }}</small>
                </span>
                <Icon name="right" class="push-right" />
              </button>
            </template>
            <template v-else>
              <h2>Instructions</h2>
              <pre class="agent-instructions">{{
                agent.instructions || 'No instructions configured.'
              }}</pre>
              <h2>
                Skills <small>{{ skills.length }}</small>
              </h2>
              <button
                v-for="skill in skills"
                :key="skill.id"
                class="work-row"
                @click="selectedSkill = skill.id"
              >
                <Icon name="book" />
                <span
                  >{{ skill.name }}<small>{{ skill.description }}</small>
                </span>
              </button>
              <p v-if="!skills.length" class="work-empty">No skills assigned</p>
              <h2>Conversation starters</h2>
              <p v-for="(suggestion, index) in agent.suggestions" :key="index" class="work-row">
                {{ suggestion }}
              </p>
              <p v-if="!agent.suggestions?.length" class="work-empty">
                Using the default research conversation starters.
              </p>
            </template>
          </main>
          <aside class="agent-summary">
            <section>
              <h2>Agent</h2>
              <dl>
                <dt>Owner</dt>
                <dd>{{ owner?.name ?? '—' }}</dd>
                <dt>Access</dt>
                <dd><Icon name="shield" :size="12" />{{ access }}</dd>
                <dt>Runtime</dt>
                <dd>{{ runtime?.name ?? '—' }}</dd>
                <dt>Model</dt>
                <dd>{{ agent.model || 'Runtime default' }}</dd>
                <dt>Concurrency</dt>
                <dd>{{ agent.concurrency ?? '—' }}</dd>
              </dl>
            </section>
            <section>
              <h2>
                Skills <small class="push-right">{{ skills.length }}</small>
              </h2>
              <p v-if="!skills.length">No skills assigned</p>
              <button
                v-for="skill in skills"
                :key="skill.id"
                class="summary-skill"
                @click="selectedSkill = skill.id"
              >
                {{ skill.name }}
              </button>
            </section>
            <section>
              <h2>Last 30 days</h2>
              <div class="agent-metrics">
                <div
                  v-for="metric in ['runs', 'success rate', 'average duration', 'failed']"
                  :key="metric"
                >
                  <strong>—</strong>
                  <small>{{ metric }}</small>
                </div>
              </div>
              <div class="metrics-chart" aria-label="Run metrics unavailable" />
            </section>
          </aside>
        </div>
      </template>
      <div v-else class="machine-content detail-rail">
        <header>
          <div>
            <h2>Runtimes</h2>
            <p>
              Built-in runtimes appear automatically. Select one for settings, or add a custom
              command.
            </p>
          </div>
          <button class="button primary compact" @click="customRuntime = true">
            <Icon name="plus" :size="14" />Add custom runtime
          </button>
        </header>
        <div class="runtime-table">
          <div class="runtime-table-head">
            <span>Runtime</span>
            <span>Health</span>
            <span>Agents</span>
            <span>Cost · 7d</span>
            <span>CLI</span>
          </div>
          <div
            v-for="item in runtimes"
            :key="item.id"
            class="runtime-row"
            role="button"
            tabindex="0"
            @click="router.push('/runtimes/' + id + '/runtime/' + item.id)"
            @keydown.enter.prevent="router.push('/runtimes/' + id + '/runtime/' + item.id)"
          >
            <span>
              <Icon name="command" :size="22" />{{ item.name
              }}<small v-if="item.provider">{{ item.provider }}</small>
            </span>
            <span>{{ item.status }}</span>
            <span>{{ state.data?.agents.filter((x) => x.runtimeId === item.id).length ?? 0 }}</span>
            <span>—</span>
            <span class="runtime-version"
              >{{ item.version ?? '—' }}
              <div class="menu-anchor">
                <button
                  class="icon-button"
                  aria-label="Runtime actions"
                  @click.stop="runtimeMenu = runtimeMenu === item.id ? '' : item.id"
                >
                  <Icon name="more" :size="14" />
                </button>
                <div v-if="runtimeMenu === item.id" class="floating-menu" @click.stop>
                  <button @click="openRuntimeTab(item.id, item.name)">
                    <Icon name="external" :size="14" />Open in new tab</button
                  ><button
                    class="runtime-delete"
                    @click="
                      unavailable('Deleting a runtime');
                      runtimeMenu = '';
                    "
                  >
                    Delete
                  </button>
                </div>
              </div></span
            >
          </div>
          <div v-if="!runtimes.length" class="work-empty">
            No runtimes reported for this machine.
          </div>
        </div>
      </div>
    </template>
    <Modal v-if="rename" title="Rename machine" @close="rename = false">
      <form class="resource-dialog-form" @submit.prevent="saveName">
        <h2>Rename machine</h2>
        <label>Name<input v-model="newName" required autofocus /> </label>
        <p v-if="error" role="alert">{{ error }}</p>
        <footer>
          <button type="button" class="button" @click="rename = false">Cancel</button>
          <button class="button primary" :disabled="busy || !newName.trim()">Save</button>
        </footer>
      </form>
    </Modal>
    <RuntimeDetail v-if="selectedRuntimeId" :id="selectedRuntimeId" /><CustomRuntime
      v-if="customRuntime"
      :machine-name="machine?.name ?? 'this machine'"
      @close="customRuntime = false"
    />
    <Modal v-if="selectedSkill" title="Skill" @close="selectedSkill = ''">
      <div class="resource-dialog-form">
        <h2>{{ skills.find((x) => x.id === selectedSkill)?.name }}</h2>
        <p>{{ skills.find((x) => x.id === selectedSkill)?.description }}</p>
        <pre class="agent-instructions">{{
          skills.find((x) => x.id === selectedSkill)?.instructions || 'No instructions.'
        }}</pre>
        <button class="button" @click="selectedSkill = ''">Close</button>
      </div>
    </Modal>
  </section>
</template>
<style scoped>
.resource-detail {
  position: absolute;
  inset: 0;
  z-index: 4;
  background: var(--surface);
  overflow: auto;
  display: flex;
  flex-direction: column;
}
.detail-rail {
  width: calc(100% - 48px);
  max-width: 1408px;
  margin: 0 auto;
}
.detail-top {
  border-bottom: 1px solid var(--border);
  padding: 12px 0 22px;
  flex: none;
}
.detail-crumb {
  display: flex;
  gap: 7px;
  align-items: center;
  font-size: 12px;
  height: 28px;
}
.detail-crumb button {
  padding: 0;
  color: var(--muted);
  font-size: 12px;
}
.resource-identity {
  display: flex;
  align-items: flex-start;
  gap: 16px;
  margin-top: 4px;
}
.resource-avatar {
  width: 56px;
  height: 56px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  background: #e1e1e6;
  flex: none;
}
.resource-avatar.machine {
  border-radius: 12px;
  background: #edecf1;
}
.identity-copy {
  min-width: 0;
  flex: 1;
}
.identity-title {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 36px;
}
.identity-title h1 {
  font-size: 24px;
  font-weight: 600;
  line-height: 32px;
  letter-spacing: -0.4px;
}
.record-status {
  font-size: 12px;
  text-transform: capitalize;
  color: var(--muted);
  white-space: nowrap;
}
.record-status.online {
  color: #287e43;
}
.identity-copy > p {
  color: var(--muted);
  line-height: 24px;
  font-size: 14px;
  max-width: 680px;
  margin-top: 2px;
}
.identity-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 16px;
  color: var(--muted);
  font-size: 12px;
  margin-top: 12px;
  min-height: 18px;
}
.identity-meta > span {
  display: flex;
  align-items: center;
  gap: 6px;
}
.detail-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
}
.detail-actions .button {
  height: 28px;
  font-weight: 400;
}
.borderless {
  border-color: transparent;
}
.stop {
  color: #e6506d;
  background: #fbe9ed;
}
.detail-tabs {
  border-bottom: 1px solid var(--border);
  flex: none;
}
.detail-tabs > .detail-rail {
  display: flex;
  gap: 24px;
  height: 44px;
}
.detail-tabs button {
  position: relative;
  padding: 0;
  font-size: 14px;
  color: var(--muted);
}
.detail-tabs button.selected {
  color: var(--text);
}
.detail-tabs button.selected:after {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  content: '';
  height: 2px;
  background: var(--text);
}
.detail-body {
  display: flex;
  gap: 24px;
  padding: 24px 0;
  align-items: flex-start;
}
.detail-body > main {
  flex: 1;
  min-width: 0;
}
.detail-body h2,
.machine-content h2 {
  font-size: 14px;
  font-weight: 500;
  line-height: 20px;
}
.detail-body h2 small {
  font-weight: 400;
  color: var(--muted);
  font-size: 11px;
  margin-left: 6px;
}
.current-work {
  border-bottom: 1px solid var(--border);
  padding-bottom: 24px;
}
.current-work > p {
  font-size: 12px;
  font-style: italic;
  color: var(--muted);
  margin-top: 12px;
}
.recent-work {
  padding-top: 24px;
}
.work-empty {
  font-size: 13px;
  color: var(--muted);
  padding: 24px 0;
}
.work-row {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 16px;
  border: 1px solid var(--border);
  border-radius: 8px;
  margin-top: 12px;
  text-align: left;
  font-size: 14px;
}
.work-row small {
  display: block;
  color: var(--muted);
  font-size: 12px;
  margin-top: 6px;
}
.agent-summary {
  flex: none;
  width: 320px;
  background: #edecf1;
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 0 20px;
}
.agent-summary > section {
  padding: 20px 0;
  border-bottom: 1px solid var(--border);
}
.agent-summary > section:last-child {
  border: 0;
}
.agent-summary h2 {
  display: flex;
  align-items: center;
  margin-bottom: 18px;
}
.agent-summary dl {
  display: grid;
  grid-template-columns: 88px minmax(0, 1fr);
  font-size: 12px;
  gap: 14px 12px;
  margin: 0;
}
.agent-summary dt {
  color: var(--muted);
}
.agent-summary dd {
  margin: 0;
  display: flex;
  gap: 5px;
  align-items: center;
  overflow-wrap: anywhere;
}
.agent-summary p {
  font-size: 12px;
  color: var(--muted);
}
.agent-metrics {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 18px;
}
.agent-metrics > div {
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.agent-metrics strong {
  font-size: 20px;
  font-weight: 500;
}
.agent-metrics small {
  font-size: 11px;
  color: var(--muted);
}
.metrics-chart {
  height: 42px;
  margin: 14px 14px 0;
  border-bottom: 1px solid #cccfd3;
}
.summary-skill {
  font-size: 12px;
  display: block;
  padding: 5px 0;
}
.agent-instructions {
  font-family: inherit;
  font-size: 13px;
  white-space: pre-wrap;
  line-height: 22px;
  padding: 16px;
  background: var(--input);
  border-radius: 8px;
  margin: 12px 0 28px;
  overflow-wrap: anywhere;
}
.detail-scroll {
  overflow: auto;
  flex: 1;
  position: relative;
}
.machine-content {
  padding-top: 24px;
}
.machine-content > header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
  gap: 12px;
}
.machine-content > header p {
  font-size: 12px;
  color: var(--muted);
  margin-top: 4px;
}
.runtime-table {
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
}
.runtime-table-head,
.runtime-row {
  display: grid;
  grid-template-columns: minmax(250px, 4fr) 1fr 100px 80px 150px;
  align-items: center;
  gap: 20px;
  padding: 0 32px;
}
.runtime-table-head {
  height: 38px;
  font-size: 12px;
  color: var(--muted);
}
.runtime-row {
  height: 48px;
  width: 100%;
  text-align: left;
  background: #edecf1;
  font-size: 12px;
}
.runtime-row:hover {
  background: #e1e1e6;
}
.runtime-row > span:first-child {
  font-size: 14px;
  display: flex;
  align-items: center;
  gap: 12px;
}
.runtime-row small {
  font-size: 11px;
  background: #dedee5;
  padding: 1px 4px;
  border-radius: 2px;
}
.runtime-table > .work-empty {
  padding: 24px 32px;
}
.resource-dialog-form {
  display: flex;
  flex-direction: column;
  gap: 20px;
  padding: 24px;
}
.resource-dialog-form h2 {
  font-size: 18px;
}
.resource-dialog-form label {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.resource-dialog-form input {
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 6px;
}
.resource-dialog-form footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.resource-dialog-form p {
  font-size: 13px;
  line-height: 20px;
}
.resource-dialog-form > .button {
  align-self: flex-start;
}
.missing-resource {
  margin: auto;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 20px;
}
@media (max-width: 1250px) {
  .detail-actions {
    flex-wrap: wrap;
    max-width: 320px;
    justify-content: flex-end;
  }
  .detail-body {
    gap: 16px;
  }
  .agent-summary {
    width: 280px;
  }
  .runtime-table-head,
  .runtime-row {
    grid-template-columns: minmax(200px, 3fr) 100px 60px 70px 100px;
    gap: 12px;
  }
}

.runtime-row {
  cursor: pointer;
}
.runtime-version {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-width: 0;
}
.runtime-table {
  overflow: visible;
}
.runtime-delete {
  color: #d62846;
}
.runtime-version .floating-menu {
  min-width: 160px;
}
</style>
