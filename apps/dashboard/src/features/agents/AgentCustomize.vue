<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useQuery, useQueryClient } from '@tanstack/vue-query';
import { api } from '../../api/client';
import { keys } from '../../api/keys';
import {
  useConnectorLibrary,
  useMe,
  useSkillLibrary,
  useWorkspaceMCPServers,
} from '../../api/queries';
import { workspaceId } from '../../api/session';
import Icon from '../../ui/Icon.vue';

type Connector = {
  name: string;
  catalogue?: string;
  workspace?: string;
  url?: string;
  command?: string[];
  env?: Record<string, string>;
};
type Team = {
  id: string;
  member: string;
  source: string;
  uploaded_by: string | null;
  team_name: string;
  skills: string[] | null;
  connectors: Connector[];
};
type Capabilities = {
  skills: string[];
  connectors: Connector[];
  teams: Team[];
  team_member: boolean;
};
const props = defineProps<{ id: string; editable: boolean }>();
const client = useQueryClient();
const me = useMe();
const library = useSkillLibrary();
const catalogue = useConnectorLibrary();
const servers = useWorkspaceMCPServers();
const query = useQuery({
  queryKey: computed(() => ['ws', workspaceId.value, 'agent-capabilities', props.id]),
  queryFn: () => api<Capabilities>('GET', `/api/agents/${props.id}/customize`),
  enabled: computed(() => !!workspaceId.value && !!props.id),
});
const selectedTeam = ref(0),
  skills = ref<string[]>([]),
  connectors = ref<Connector[]>([]),
  search = ref(''),
  skillPickerOpen = ref(false),
  connectorPickerOpen = ref(false),
  busy = ref(false),
  error = ref('');
watch(
  () => [workspaceId.value, props.id],
  () => {
    selectedTeam.value = 0;
    skillPickerOpen.value = false;
    connectorPickerOpen.value = false;
    error.value = '';
  },
);
const team = computed(() => query.data.value?.teams[selectedTeam.value]);
const canEdit = computed(() =>
  team.value
    ? team.value.source !== 'shipped' && team.value.uploaded_by === me.data.value?.id
    : !query.data.value?.team_member && props.editable,
);
const options = computed(() =>
  [...new Set([...skills.value, ...(library.data.value ?? []).map((s) => s.name)])]
    .sort()
    .filter((name) => name.toLowerCase().includes(search.value.toLowerCase())),
);
function resetSelection() {
  skills.value = [...(team.value ? (team.value.skills ?? []) : (query.data.value?.skills ?? []))];
  connectors.value = (
    team.value ? (team.value.connectors ?? []) : (query.data.value?.connectors ?? [])
  ).map((c) => ({ ...c }));
  error.value = '';
}
watch([() => query.data.value, selectedTeam], resetSelection, { immediate: true });
function cancelPicker() {
  skillPickerOpen.value = false;
  connectorPickerOpen.value = false;
  resetSelection();
}
function addConnector(event: Event) {
  const select = event.target as HTMLSelectElement;
  const value = select.value;
  if (value.startsWith('workspace:')) {
    const server = servers.data.value?.find((s) => s.id === value.slice(10));
    if (
      server &&
      !connectors.value.some((c) => c.workspace === server.id || c.name === server.name)
    )
      connectors.value.push({ name: server.name, workspace: server.id });
  } else if (value && !connectors.value.some((c) => c.name === value)) {
    connectors.value.push({ name: value, catalogue: value });
  }
  select.value = '';
}
async function save(kind: 'skills' | 'connectors') {
  if (busy.value || !canEdit.value) return;
  const ws = workspaceId.value,
    id = props.id,
    currentTeam = team.value;
  busy.value = true;
  error.value = '';
  try {
    if (currentTeam) {
      await api(
        'POST',
        `/api/teams/${currentTeam.id}/${kind}`,
        { member: currentTeam.member, [kind]: kind === 'skills' ? skills.value : connectors.value },
        undefined,
        { workspace: ws },
      );
    } else {
      await api(
        'PUT',
        `/api/agents/${id}/customize`,
        { skills: skills.value, connectors: connectors.value },
        undefined,
        { workspace: ws },
      );
    }
    await Promise.all([
      query.refetch(),
      client.invalidateQueries({ queryKey: keys.packs(ws) }),
      client.invalidateQueries({ queryKey: keys.agents(ws) }),
    ]);
    if (kind === 'skills') skillPickerOpen.value = false;
    else connectorPickerOpen.value = false;
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <section class="agent-pane agent-customize">
    <h2>Customize</h2>
    <p class="agent-lede">
      Choose the skills and connectors this agent uses in future studies.
      <span v-if="!query.isPending.value && !query.isError.value && !canEdit && team?.source === 'shipped'">Shipped Team capabilities are read-only.</span>
    </p>
    <p v-if="query.isPending.value" role="status">Loading capabilities…</p>
    <p v-else-if="query.isError.value" role="alert">
      Could not load capabilities. <button type="button" @click="query.refetch()">Retry</button>
    </p>
    <template v-else>
      <label v-if="(query.data.value?.teams.length ?? 0) > 1"
        >Team
        <select v-model="selectedTeam">
          <option
            v-for="(t, index) in query.data.value?.teams"
            :key="t.id + t.member"
            :value="index"
          >
            {{ t.team_name }} · {{ t.member }}
          </option>
        </select>
      </label>
      <p v-if="query.data.value?.team_member && !team" class="agent-hint">
        This agent's Team is not available to you.
      </p>
      <p v-else-if="!canEdit && team?.source !== 'shipped'" class="agent-hint">
        Only the owner can change these capabilities.
      </p>
      <section class="capability-section" aria-labelledby="skills-heading">
        <div class="capability-heading">
          <div>
            <h3 id="skills-heading">Skills</h3>
          </div>
          <button
            type="button"
            class="button capability-add"
            :disabled="!canEdit || busy || library.isPending.value || library.isError.value"
            :aria-expanded="skillPickerOpen"
            @click="
              connectorPickerOpen = false;
              skillPickerOpen = true;
            "
          >
            <Icon name="plus" :size="14" /> Add skill
          </button>
        </div>
        <p v-if="library.isError.value" role="alert">
          Could not load the skills library.
          <button type="button" @click="library.refetch()">Retry</button>
        </p>
        <div v-if="!skills.length" class="capability-empty">
          <Icon name="file" :size="28" /><strong>No skills assigned</strong
          ><span>{{
            canEdit
              ? 'Add skills to give this agent reusable knowledge.'
              : 'This agent has no assigned skills.'
          }}</span>
        </div>
        <ul v-else class="capability-rows">
          <li v-for="skill in skills" :key="skill">
            <Icon name="book" :size="16" /><span>{{ skill }}</span>
          </li>
        </ul>
        <div v-if="skillPickerOpen" class="capability-picker">
          <label class="picker-label" for="agent-skill-search">Choose skills</label>
          <input
            id="agent-skill-search"
            v-model="search"
            type="search"
            placeholder="Search skills…"
            aria-label="Search skills"
          />
          <fieldset class="capability-list" :disabled="busy" aria-label="Skills">
            <label v-for="skill in options" :key="skill"
              ><input v-model="skills" type="checkbox" :value="skill" />{{ skill }}</label
            >
          </fieldset>
          <div class="picker-actions">
            <button type="button" class="button" :disabled="busy" @click="cancelPicker">
              Cancel</button
            ><button type="button" class="button primary" :disabled="busy" @click="save('skills')">
              Save skills
            </button>
          </div>
        </div>
      </section>
      <section class="capability-section" aria-labelledby="connectors-heading">
        <div class="capability-heading">
          <div>
            <h3 id="connectors-heading">Connectors</h3>
          </div>
          <button
            type="button"
            class="button capability-add"
            :disabled="
              !canEdit ||
              busy ||
              catalogue.isPending.value ||
              servers.isPending.value ||
              catalogue.isError.value ||
              servers.isError.value
            "
            :aria-expanded="connectorPickerOpen"
            @click="
              skillPickerOpen = false;
              connectorPickerOpen = true;
            "
          >
            <Icon name="plus" :size="14" /> Add connectors
          </button>
        </div>
        <p v-if="catalogue.isError.value || servers.isError.value" role="alert">
          Could not load the connector library.
          <button
            type="button"
            @click="
              catalogue.refetch();
              servers.refetch();
            "
          >
            Retry
          </button>
        </p>
        <div v-if="!connectors.length" class="capability-empty">
          <Icon name="plug" :size="28" /><strong>No connectors assigned</strong
          ><span>{{
            canEdit
              ? 'Add connectors to give this agent access to data sources.'
              : 'This agent has no assigned connectors.'
          }}</span>
        </div>
        <ul v-else class="capability-rows">
          <li v-for="(connector, index) in connectors" :key="connector.name">
            <Icon name="plug" :size="16" /><span>{{ connector.name || 'Unnamed connector' }}</span
            ><button
              v-if="connectorPickerOpen"
              type="button"
              :disabled="busy"
              :aria-label="`Remove connector ${connector.name}`"
              @click="connectors.splice(index, 1)"
            >
              <Icon name="close" :size="14" />
            </button>
          </li>
        </ul>
        <div v-if="connectorPickerOpen" class="capability-picker">
          <label class="picker-label" for="agent-connector-select">Choose connectors</label>
          <select
            id="agent-connector-select"
            aria-label="Add connector"
            :disabled="busy"
            @change="addConnector"
          >
            <option value="">Select a connector…</option>
            <optgroup label="Catalogue">
              <option v-for="c in catalogue.data.value ?? []" :key="c.name" :value="c.name">
                {{ c.name }}
              </option>
            </optgroup>
            <optgroup label="Workspace">
              <option
                v-for="c in servers.data.value ?? []"
                :key="c.id"
                :value="`workspace:${c.id}`"
              >
                {{ c.name }}
              </option>
            </optgroup>
          </select>
          <div class="picker-actions">
            <button type="button" class="button" :disabled="busy" @click="cancelPicker">
              Cancel</button
            ><button
              type="button"
              class="button primary"
              :disabled="busy"
              @click="save('connectors')"
            >
              Save connectors
            </button>
          </div>
        </div>
      </section>
      <p v-if="error" role="alert" class="inline-error">{{ error }}</p>
    </template>
  </section>
</template>

<style scoped>
.agent-customize h2 {
  margin-bottom: 20px;
}
.agent-customize .agent-lede {
  margin-bottom: 16px;
}
.capability-section {
  margin-top: 34px;
}
.capability-section[aria-labelledby="skills-heading"] {
  margin-top: 16px;
}
.capability-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  margin-bottom: 16px;
}
.agent-customize .capability-heading h3 {
  margin: 0 0 6px;
}
.capability-heading p {
  margin: 0;
  color: var(--muted);
  font-size: 13px;
  line-height: 1.5;
}
.capability-add {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: 5px;
}
.capability-empty {
  min-height: 165px;
  display: flex;
  flex-direction: column;
  justify-content: center;
  align-items: center;
  gap: 8px;
  text-align: center;
  padding: 20px;
  border: 1px dashed var(--border);
  border-radius: 10px;
  color: var(--muted);
}
.capability-empty strong {
  font-size: 13px;
  color: var(--text);
  font-weight: 500;
}
.capability-empty span {
  font-size: 13px;
  line-height: 1.5;
}
.capability-rows {
  list-style: none;
  padding: 0;
  margin: 0;
  border: 1px solid var(--border);
  border-radius: 10px;
  overflow: hidden;
}
.capability-rows li {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 14px;
  font-size: 13px;
}
.capability-rows li + li {
  border-top: 1px solid var(--border);
}
.capability-rows li > span {
  flex: 1;
  overflow-wrap: anywhere;
}
.capability-rows li > svg {
  color: var(--muted);
  flex: none;
}
.capability-picker {
  margin-top: 12px;
  padding: 16px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--input);
}
.picker-label {
  display: block;
  font-size: 13px;
  font-weight: 500;
}
.agent-customize input[type='search'],
.agent-customize select {
  display: block;
  width: 100%;
  margin: 10px 0;
  padding: 8px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--surface);
  color: var(--text);
}
.capability-list {
  display: grid;
  gap: 8px;
  max-height: 250px;
  overflow-y: auto;
  border: 0;
  padding: 8px 0;
}
.capability-list label {
  display: flex;
  align-items: center;
  gap: 8px;
}
.picker-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 14px;
}
@media (max-width: 600px) {
  .capability-heading {
    flex-wrap: wrap;
  }
}
</style>
