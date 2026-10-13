<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { Terminal } from '@lucide/vue';
import type { Agent, ResourceCreateMap } from '../../../../packages/contracts/src/research';
import { state, service, refresh, notify } from '../services/workspace';
import Icon from './Icon.vue';
import AgentConfiguration from './AgentConfiguration.vue';
import ResourceEditor from './ResourceEditor.vue';
const props = defineProps<{ agent: Agent; tab: string }>();
const section = ref(props.tab === 'Capabilities' ? 'Instructions' : 'General'),
  instructions = ref(props.agent.instructions ?? ''),
  customArgs = ref([...(props.agent.customArgs ?? [])]),
  error = ref(''),
  busy = ref(false),
  adding = ref(false),
  skillSelection = ref([...(props.agent.skillIds ?? [])]),
  connectorDialog = ref(false);
const nav = computed(() =>
  props.tab === 'Capabilities'
    ? ['Instructions', 'Skills', 'Connectors']
    : ['General', 'Access', 'Environment', 'Custom Args'],
);
const skills = computed(
  () => state.data?.skills.filter((x) => props.agent.skillIds?.includes(x.id)) ?? [],
);
const connectors = computed(
  () => state.data?.connectors.filter((x) => props.agent.connectorIds?.includes(x.id)) ?? [],
);
const runtime = computed(() => state.data?.runtimes.find((x) => x.id === props.agent.runtimeId));
watch(
  () => props.tab,
  () => {
    section.value = nav.value[0]!;
    error.value = '';
  },
);
async function save(patch: Partial<ResourceCreateMap['agents']>) {
  busy.value = true;
  error.value = '';
  try {
    await service.value.updateResource('agents', props.agent.id, patch);
    await refresh();
    notify('Agent configuration saved.');
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
function toggleSkill(id: string) {
  const current = props.agent.disabledSkillIds ?? [];
  void save({
    disabledSkillIds: current.includes(id) ? current.filter((x) => x !== id) : [...current, id],
  });
}
function toggleConnector(id: string) {
  const current = props.agent.connectorIds ?? [];
  void save({
    connectorIds: current.includes(id) ? current.filter((x) => x !== id) : [...current, id],
  });
}
</script>
<template>
  <div class="agent-panels">
    <nav aria-label="Agent configuration sections">
      <button
        v-for="item in nav"
        :key="item"
        :class="{ selected: section === item }"
        @click="
          section = item;
          error = '';
        "
      >
        {{ item }}
      </button>
    </nav>
    <main>
      <div class="agent-panel-content">
        <h2>{{ section }}</h2>
        <AgentConfiguration
          v-if="section === 'General' || section === 'Access'"
          :key="agent.id + section"
          :agent="agent"
          :section="section === 'General' ? 'general' : 'access'"
        />
        <form v-else-if="section === 'Instructions'" @submit.prevent="save({ instructions })">
          <p>Set the system prompt used for every run. Markdown is supported.</p>
          <label
            >System prompt<textarea
              v-model="instructions"
              aria-label="System prompt"
              class="system-prompt"
            />
          </label>
          <footer>
            <button class="button primary compact" :disabled="busy">Save</button>
          </footer>
        </form>
        <template v-else-if="section === 'Skills'">
          <header>
            <div>
              <h3>Assigned to agent</h3>
              <p>Turn off a skill to pause its use without removing it.</p>
            </div>
            <button class="button compact" @click="adding = !adding">
              <Icon name="plus" :size="14" />Add skill
            </button>
          </header>
          <div v-if="adding" class="selection-box">
            <label v-for="skill in state.data?.skills" :key="skill.id">
              <input v-model="skillSelection" type="checkbox" :value="skill.id" />{{
                skill.name
              }}</label
            >
            <small v-if="!state.data?.skills.length">No workspace skills available.</small>
            <button
              class="button compact"
              @click="
                save({ skillIds: skillSelection });
                adding = false;
              "
            >
              Apply
            </button>
          </div>
          <div v-if="!skills.length" class="panel-empty tall">
            <Icon name="book" :size="22" />
            <span>No skills assigned</span>
          </div>
          <div v-else class="panel-records">
            <div v-for="skill in skills" :key="skill.id">
              <span class="panel-icon">
                <Icon name="book" />
              </span>
              <span
                >{{ skill.name }}<small>{{ skill.description }}</small>
              </span>
              <button
                class="panel-toggle push-right"
                role="switch"
                :aria-checked="!agent.disabledSkillIds?.includes(skill.id)"
                :class="{ on: !agent.disabledSkillIds?.includes(skill.id) }"
                :aria-label="'Enable ' + skill.name"
                @click="toggleSkill(skill.id)"
              >
                <span />
              </button>
            </div>
          </div>
          <header>
            <div>
              <h3>Inherited from runtime</h3>
              <p>
                {{
                  runtime
                    ? 'Discovered from ' + runtime.name + '.'
                    : 'Select a runtime to discover inherited skills.'
                }}
              </p>
            </div>
            <button
              class="text-button"
              @click="notify('Runtime skill discovery requires a connected inventory adapter.')"
            >
              <Icon name="refresh" :size="14" />Refresh
            </button>
          </header>
          <div class="panel-empty">Runtime skill inventory is unavailable.</div>
        </template>
        <template v-else-if="section === 'Connectors'">
          <header>
            <h3>Managed by Lykeion</h3>
            <button class="button compact" @click="connectorDialog = true">
              <Icon name="plus" :size="14" />Add connector
            </button>
          </header>
          <div v-if="!connectors.length" class="panel-empty">
            <Icon name="plug" :size="16" />No connectors are managed by Lykeion yet.
          </div>
          <div v-else class="panel-records">
            <div v-for="connector in connectors" :key="connector.id">
              <Icon name="link" />
              <span
                >{{ connector.name
                }}<small>{{ connector.provider }} · {{ connector.status }}</small>
              </span>
            </div>
          </div>
          <h3>From the workspace library</h3>
          <div v-if="!state.data?.connectors.length" class="panel-empty">
            <Icon name="plug" :size="16" />This workspace has no connectors to assign. Add them in
            Connectors.
          </div>
          <div v-else class="selection-box">
            <label v-for="connector in state.data.connectors" :key="connector.id">
              <input
                type="checkbox"
                :checked="agent.connectorIds?.includes(connector.id)"
                @change="toggleConnector(connector.id)"
              />{{ connector.name }}</label
            >
          </div>
          <header>
            <h3>Inherited from {{ runtime?.name ?? 'runtime' }}</h3>
            <button
              class="text-button"
              @click="notify('Runtime connector discovery requires a connected inventory adapter.')"
            >
              <Icon name="refresh" :size="14" />Refresh
            </button>
          </header>
          <div class="panel-empty">Runtime connector inventory is unavailable.</div>
        </template>
        <template v-else-if="section === 'Environment'">
          <header class="environment-heading">
            <div>
              <h3><Icon name="shield" :size="14" />Environment values are masked.</h3>
              <p>Revealing or editing values requires the control plane's audited secret store.</p>
            </div>
            <button
              class="button compact"
              @click="
                error =
                  'The audited environment adapter is unavailable. No values have been revealed.'
              "
            >
              Reveal &amp; edit
            </button>
          </header>
        </template>
        <form v-else-if="section === 'Custom Args'" @submit.prevent="save({ customArgs })">
          <p>Add CLI arguments passed when this agent starts.</p>
          <header>
            <div>
              <h3>Arguments</h3>
              <p>Each item is passed as one token. The list order is preserved.</p>
            </div>
            <button type="button" class="button compact" @click="customArgs.push('')">
              <Icon name="plus" :size="14" />Add argument
            </button>
          </header>
          <div class="args-box">
            <div v-if="!customArgs.length" class="args-empty">
              <span class="panel-icon">
                <Terminal :size="16" :stroke-width="1.7" aria-hidden="true" />
              </span>
              <span>No arguments yet</span>
            </div>
            <div v-for="(_, i) in customArgs" :key="i" class="arg-row">
              <input v-model="customArgs[i]" :aria-label="'Argument ' + (i + 1)" />
              <button
                type="button"
                class="icon-button"
                :aria-label="'Remove argument ' + (i + 1)"
                @click="customArgs.splice(i, 1)"
              >
                <Icon name="close" :size="14" />
              </button>
            </div>
          </div>
          <h3>Command preview</h3>
          <div class="command-preview">
            <span class="panel-icon">
              <Terminal :size="16" :stroke-width="1.7" aria-hidden="true" />
            </span>
            <code
              >{{ runtime?.provider ?? '[runtime command]' }}
              {{ customArgs.map((x) => JSON.stringify(x)).join(' ') }}</code
            >
          </div>
          <footer>
            <button class="button primary compact" :disabled="busy">Save</button>
          </footer>
        </form>
        <p v-if="error" class="panel-error" role="alert">{{ error }}</p>
      </div>
    </main>
    <ResourceEditor v-if="connectorDialog" kind="connector" @close="connectorDialog = false" />
  </div>
</template>
<style scoped>
.agent-panels {
  display: flex;
  flex: 1;
  min-height: 0;
  width: calc(100% - 48px);
  max-width: 1408px;
  margin: 0 auto;
}
.agent-panels > nav {
  width: 192px;
  flex: none;
  border-right: 1px solid var(--border);
  padding: 16px 16px 0 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.agent-panels > nav > button {
  height: 32px;
  text-align: left;
  border-radius: 6px;
  padding: 0 10px;
  font-size: 12px;
  color: var(--muted);
}
.agent-panels > nav > button.selected {
  background: var(--selected);
  color: var(--text);
}
.agent-panels > main {
  flex: 1;
  min-width: 0;
  overflow: auto;
  padding: 32px;
}
.agent-panel-content {
  max-width: 704px;
  display: flex;
  flex-direction: column;
  gap: 28px;
}
.agent-panel-content > h2 {
  font-size: 16px;
  font-weight: 500;
  line-height: 22px;
}
.agent-panel-content h3 {
  font-size: 14px;
  font-weight: 500;
  line-height: 20px;
}
.agent-panel-content p {
  font-size: 14px;
  line-height: 21px;
  color: var(--muted);
}
.agent-panel-content > form {
  display: flex;
  flex-direction: column;
  gap: 28px;
}
.agent-panel-content label {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 14px;
}
.agent-panel-content .system-prompt {
  width: 100%;
  min-height: 510px;
  resize: vertical;
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 10px;
  line-height: 24px;
  background: var(--surface);
  font-size: 14px;
}
.agent-panel-content footer {
  display: flex;
  justify-content: flex-end;
}
.agent-panel-content header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}
.agent-panel-content header p {
  font-size: 12px;
  line-height: 18px;
  margin-top: 4px;
}
.agent-panel-content .panel-empty {
  border: 1px dashed var(--border);
  border-radius: 8px;
  padding: 24px 16px;
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 12px;
  color: var(--muted);
  margin-top: -12px;
}
.agent-panel-content .panel-empty.tall {
  height: 138px;
  justify-content: center;
  flex-direction: column;
  font-size: 14px;
  gap: 16px;
}
.selection-box {
  padding: 16px;
  border: 1px solid var(--border);
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.selection-box > label {
  flex-direction: row;
  align-items: center;
  gap: 8px;
}
.selection-box .button {
  align-self: flex-end;
}
.selection-box small {
  font-size: 12px;
  color: var(--muted);
}
.panel-records {
  border: 1px solid var(--border);
  border-radius: 8px;
  background: color-mix(in srgb, var(--input) 60%, var(--surface));
  overflow: hidden;
}
.panel-records > div {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px;
  border-bottom: 1px solid var(--border);
  font-size: 14px;
}
.panel-records small {
  display: block;
  font-size: 12px;
  color: var(--muted);
  line-height: 18px;
}
.panel-icon {
  width: 36px;
  height: 36px;
  display: grid;
  place-items: center;
  flex: none;
  background: var(--selected);
  border-radius: 7px;
  color: var(--muted);
}
.panel-toggle {
  width: 32px;
  height: 18px;
  border-radius: 12px;
  background: var(--selected);
  display: flex;
  align-items: center;
  padding: 1px;
  flex: none;
}
.panel-toggle > span {
  width: 16px;
  height: 16px;
  background: white;
  border-radius: 50%;
}
.panel-toggle.on {
  background: var(--primary);
  justify-content: flex-end;
}
.agent-panel-content .text-button {
  color: var(--text);
  display: flex;
  align-items: center;
  gap: 4px;
  white-space: nowrap;
}
.environment-heading h3 {
  display: flex;
  align-items: center;
  gap: 8px;
}
.args-box {
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 16px;
  margin-top: -16px;
}
.args-empty {
  height: 84px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  gap: 14px;
  font-size: 14px;
}
.arg-row {
  display: flex;
  gap: 8px;
  padding: 6px 0;
}
.arg-row input {
  flex: 1;
  min-width: 0;
  border: 1px solid var(--border);
  background: var(--surface);
  border-radius: 5px;
  padding: 8px;
}
.command-preview {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px;
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 12px;
  margin-top: -16px;
}
.command-preview code {
  font-size: 12px;
  overflow-wrap: anywhere;
}
.agent-panel-content > .panel-error {
  color: var(--error-text-panel);
  font-size: 13px;
}
</style>
