<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useQueryClient } from '@tanstack/vue-query';
import Icon from '../components/Icon.vue';
import { backendLabel } from '../lib/backends';
import { api } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import {
  useAgents,
  useConnectorLibrary,
  useMe,
  useSkillLibrary,
  useWorkspaceMCPServers,
} from '../api/queries';

type Conn = {
  kind: 'catalogue' | 'workspace' | 'url' | 'command';
  name: string;
  value: string;
  env: string;
};
type Slot = { name: string; agent: string; role: string; skills: string[]; connectors: Conn[] };

const MAX_MEMBERS = 12;
const MAX_CRITERIA = 10;
const stages = ['grounding', 'hypotheses', 'experimentation', 'validation', 'reporting'];
const core = [
  'Does the work cite where its facts came from?',
  'Does it answer what the researcher asked?',
  "Does it say what it couldn't do?",
];
const memberName = /^[a-z][a-z0-9-]{0,31}$/;
const secretEnv = /(key|token|secret|password|passwd|credential|auth)/i;

const router = useRouter();
const client = useQueryClient();
const me = useMe();
const agents = useAgents();
const skills = useSkillLibrary();
const catalogue = useConnectorLibrary();
const workspaceConnectors = useWorkspaceMCPServers();

const name = ref('');
const description = ref('');
const domain = ref('');
const stage = ref('');
const leader = ref({ agent: '', role: '' });
const auditor = ref({ agent: '', role: '' });
const blank = (): Slot => ({ name: '', agent: '', role: '', skills: [], connectors: [] });
const members = ref<Slot[]>([blank()]);
const criteria = ref<string[]>([]);
const steps = ref(12);
const busy = ref(false);
const error = ref('');

// A private agent is offered only to its owner, as the server also decides.
const usable = computed(() =>
  (agents.data.value ?? []).filter(
    (a) => !a.archived_at && (a.visibility === 'workspace' || a.owner_id === me.data.value?.id),
  ),
);
const agentById = (id: string) => usable.value.find((a) => a.id === id);
const same = (a?: { harness: string; model: string }, b?: { harness: string; model: string }) =>
  !!a && !!b && a.harness === b.harness && a.model === b.model;
const auditorClash = computed(() => {
  const a = agentById(auditor.value.agent);
  return !!a && [leader.value, ...members.value].some((s) => same(a, agentById(s.agent)));
});

function connectorProblem(c: Conn): string {
  if (c.kind === 'workspace')
    return workspaceConnectors.data.value?.some((item) => item.id === c.value) ? '' : 'unavailable';
  if (c.kind === 'catalogue')
    return catalogue.data.value?.some((item) => item.name === c.name) ? '' : 'unavailable';
  if (!c.name.trim()) return 'name';
  if (c.kind === 'url') {
    try {
      const u = new URL(c.value);
      return u.protocol === 'https:' && u.host ? '' : 'url';
    } catch {
      return 'url';
    }
  }
  const bin = c.value.trim().split(/\s+/)[0] ?? '';
  if (!bin || bin.includes('/') || bin.includes('..')) return 'command';
  return envPairs(c).some(([k]) => secretEnv.test(k)) ? 'env' : '';
}
const envPairs = (c: Conn) =>
  c.env
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean)
    .map((l) => [l.split('=')[0]!.trim(), l.slice(l.indexOf('=') + 1)] as [string, string]);

const names = computed(() => members.value.map((m) => m.name));
const problem = computed(() => {
  if (!name.value.trim()) return 'Name the Team.';
  if (!leader.value.agent || !leader.value.role.trim())
    return 'The leader needs an agent and a role.';
  if (!auditor.value.agent || !auditor.value.role.trim())
    return 'The auditor needs an agent and a role.';
  for (const m of members.value) {
    if (!memberName.test(m.name)) return 'A member name is lowercase letters, digits and hyphens.';
    if (
      names.value.filter((n) => n === m.name).length > 1 ||
      m.name === 'leader' ||
      m.name === 'auditor'
    )
      return `The member name ${m.name} is used twice.`;
    if (!m.agent || !m.role.trim()) return `${m.name} needs an agent and a role.`;
    if (m.skills.some((name) => !skills.data.value?.some((item) => item.name === name)))
      return `Check ${m.name}'s unavailable skills.`;
    if (m.connectors.some((c) => connectorProblem(c))) return `Check ${m.name}'s connectors.`;
  }
  if (criteria.value.some((c) => !c.trim() || c.length > 200))
    return 'Each criterion is 1 to 200 characters.';
  if (!(steps.value >= 1 && steps.value <= 50)) return 'Steps per column is 1 to 50.';
  return '';
});
const canCreate = computed(() => !problem.value && !auditorClash.value && !busy.value);

const label = (s: string) => `${s[0]!.toUpperCase()}${s.slice(1)}`;
const harnessName = backendLabel;

function addConnector(m: Slot, pick: string) {
  if (!pick) return;
  if (pick.startsWith('workspace:')) {
    const id = pick.slice(10);
    const item = workspaceConnectors.data.value?.find((c) => c.id === id);
    if (item) m.connectors.push({ kind: 'workspace', name: item.name, value: item.id, env: '' });
    return;
  }
  const kind = pick === 'url' || pick === 'command' ? pick : 'catalogue';
  m.connectors.push({ kind, name: kind === 'catalogue' ? pick : '', value: '', env: '' });
}

async function create() {
  if (!canCreate.value) return;
  busy.value = true;
  error.value = '';
  const workspace = workspaceId.value;
  const slot = (
    s: { name?: string; agent: string; role: string; skills?: string[]; connectors?: Conn[] },
    fallback: string,
  ) => ({
    name: s.name ?? fallback,
    role: s.role.trim(),
    agent_id: s.agent,
    skills: s.skills ?? [],
    connectors: (s.connectors ?? []).map((c) =>
      c.kind === 'workspace'
        ? { name: c.name, workspace: c.value }
        : c.kind === 'catalogue'
          ? { name: c.name, catalogue: c.name }
          : c.kind === 'url'
            ? { name: c.name.trim(), url: c.value.trim() }
            : {
                name: c.name.trim(),
                command: c.value.trim().split(/\s+/),
                ...(c.env.trim() ? { env: Object.fromEntries(envPairs(c)) } : {}),
              },
    ),
  });
  try {
    const made = await api<{ id: string }>('POST', '/api/teams', {
      name: name.value.trim(),
      description: description.value.trim(),
      domain: domain.value.trim(),
      stage: stage.value,
      leader: slot(leader.value, 'leader'),
      auditor: slot(auditor.value, 'auditor'),
      members: members.value.map((m) => slot(m, m.name)),
      criteria: criteria.value.map((c) => c.trim()),
      steps: steps.value,
    });
    await Promise.all([
      client.invalidateQueries({ queryKey: keys.packs(workspace) }),
      client.invalidateQueries({ queryKey: keys.agents(workspace) }),
    ]);
    await router.push('/teams/' + made.id);
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <section class="directory-page">
    <form class="team-form" @submit.prevent="create">
      <h1>New Team</h1>

      <h2>Identity</h2>
      <input
        v-model="name"
        aria-label="Team name"
        placeholder="Name, for example scRNA-seq evidence brief"
        maxlength="64"
      />
      <input
        v-model="description"
        aria-label="Description"
        placeholder="What this Team does, in one or two sentences"
      />
      <div class="row">
        <input
          v-model="domain"
          aria-label="Domain"
          placeholder="Domain, for example Computational biology"
        />
        <select v-model="stage" aria-label="Built for">
          <option value="">Built for a set of stages</option>
          <option v-for="s in stages" :key="s" :value="s">Built for {{ label(s) }}</option>
        </select>
      </div>

      <h2>Leader</h2>
      <div class="slot">
        <select v-model="leader.agent" aria-label="Leader agent">
          <option value="" disabled>Choose an agent</option>
          <option v-for="a in usable" :key="a.id" :value="a.id">{{ a.name }}</option>
        </select>
        <small v-if="agentById(leader.agent)">{{
          harnessName(agentById(leader.agent)!.harness)
        }}</small>
        <textarea
          v-model="leader.role"
          aria-label="Leader role"
          placeholder="Role description: what the leader decides and who it names for each step"
        />
      </div>

      <h2>Members</h2>
      <div v-for="(m, i) in members" :key="i" class="slot team-member">
        <div class="row">
          <input
            v-model="m.name"
            aria-label="Member name"
            placeholder="Name, for example bioinformatician"
            maxlength="32"
          />
          <select v-model="m.agent" aria-label="Member agent">
            <option value="" disabled>Choose an agent</option>
            <option v-for="a in usable" :key="a.id" :value="a.id">{{ a.name }}</option>
          </select>
          <button
            type="button"
            class="button"
            :disabled="members.length < 2"
            :aria-label="`Remove member ${i + 1}`"
            @click="members.splice(i, 1)"
          >
            <Icon name="close" :size="14" />
          </button>
        </div>
        <small v-if="agentById(m.agent)">{{ harnessName(agentById(m.agent)!.harness) }}</small>
        <textarea
          v-model="m.role"
          aria-label="Member role"
          placeholder="Role description (the leader reads it)"
        />
        <div class="row chips">
          <span class="muted">Skills</span>
          <button
            v-for="s in m.skills"
            :key="s"
            type="button"
            class="button chip"
            :aria-label="`Remove skill ${s}`"
            @click="m.skills.splice(m.skills.indexOf(s), 1)"
          >
            {{ s }} ×
          </button>
          <select
            aria-label="Add skill"
            @change="
              (e) => {
                const t = e.target as HTMLSelectElement;
                if (t.value && !m.skills.includes(t.value)) m.skills.push(t.value);
                t.value = '';
              }
            "
          >
            <option value="">+ skill</option>
            <option v-for="s in skills.data.value ?? []" :key="s.name" :value="s.name">
              {{ s.name }}
            </option>
          </select>
        </div>
        <div class="row chips">
          <span class="muted">Connectors</span>
          <select
            aria-label="Add connector"
            @change="
              (e) => {
                const t = e.target as HTMLSelectElement;
                addConnector(m, t.value);
                t.value = '';
              }
            "
          >
            <option value="">+ connector</option>
            <optgroup label="Catalogue">
              <option
                v-for="c in catalogue.data.value ?? []"
                :key="c.name"
                :value="c.name"
                :disabled="m.connectors.some((x) => x.name === c.name)"
              >
                {{ c.name }}
              </option>
            </optgroup>
            <optgroup label="Workspace">
              <option
                v-for="c in workspaceConnectors.data.value ?? []"
                :key="c.id"
                :value="'workspace:' + c.id"
                :disabled="m.connectors.some((x) => x.kind === 'workspace' && x.value === c.id)"
              >
                {{ c.name }}
              </option>
            </optgroup>
            <optgroup label="Your own">
              <option value="url">An https MCP URL</option>
              <option value="command">A local command</option>
            </optgroup>
          </select>
        </div>
        <div v-for="(c, j) in m.connectors" :key="j" class="row connector">
          <template v-if="c.kind === 'catalogue' || c.kind === 'workspace'">
            <span class="chip-text"
              >{{ c.name
              }}<small v-if="connectorProblem(c)" class="inline-error"> · Unavailable</small></span
            >
          </template>
          <template v-else>
            <input v-model="c.name" aria-label="Connector name" placeholder="Name" />
            <input
              v-if="c.kind === 'url'"
              v-model="c.value"
              aria-label="Connector URL"
              placeholder="https://"
            />
            <template v-else>
              <input
                v-model="c.value"
                aria-label="Connector command"
                placeholder="program and arguments"
              />
              <textarea
                v-model="c.env"
                aria-label="Connector environment"
                placeholder="NAME=value, one per line (no secrets)"
              />
            </template>
            <small
              v-if="connectorProblem(c) && connectorProblem(c) !== 'name'"
              class="inline-error"
            >
              {{
                connectorProblem(c) === 'url'
                  ? 'An https URL is required'
                  : connectorProblem(c) === 'env'
                    ? 'An environment name looks like a secret'
                    : 'Name a program, not a path'
              }}
            </small>
          </template>
          <button
            type="button"
            class="button"
            :aria-label="`Remove connector ${c.name}`"
            @click="m.connectors.splice(j, 1)"
          >
            <Icon name="close" :size="14" />
          </button>
        </div>
      </div>
      <button
        type="button"
        class="button"
        :disabled="members.length >= MAX_MEMBERS"
        @click="members.push(blank())"
      >
        + Add member
      </button>

      <h2>Auditor</h2>
      <div class="slot">
        <select v-model="auditor.agent" aria-label="Auditor agent">
          <option value="" disabled>Choose an agent</option>
          <option v-for="a in usable" :key="a.id" :value="a.id">{{ a.name }}</option>
        </select>
        <small v-if="agentById(auditor.agent)"
          >{{ harnessName(agentById(auditor.agent)!.harness) }} · runs once after the leader says
          done, before scoring</small
        >
        <p v-if="auditorClash" class="inline-error" role="alert">
          The auditor must run on a different model from the leader and every member
        </p>
        <textarea
          v-model="auditor.role"
          aria-label="Auditor role"
          placeholder="Instructions: check every quote against its recorded call"
        />
      </div>

      <h2>Scoring criteria</h2>
      <ul class="criteria">
        <li v-for="c in core" :key="c" class="core-criterion">
          {{ c }}
        </li>
        <li v-for="(_, i) in criteria" :key="i" class="row">
          <input v-model="criteria[i]" :aria-label="`Criterion ${i + 1}`" maxlength="200" />
          <button
            type="button"
            class="button"
            :aria-label="`Remove criterion ${i + 1}`"
            @click="criteria.splice(i, 1)"
          >
            <Icon name="close" :size="14" />
          </button>
        </li>
      </ul>
      <button
        type="button"
        class="button"
        :disabled="criteria.length >= MAX_CRITERIA"
        @click="criteria.push('')"
      >
        + This Team's own criterion
      </button>

      <h2>Limits</h2>
      <label class="steps"
        >Steps per column<input
          v-model.number="steps"
          type="number"
          min="1"
          max="50"
          aria-label="Steps per column"
      /></label>

      <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
      <footer>
        <button type="button" class="button" @click="router.push('/teams')">Cancel</button>
        <button class="button primary" :disabled="!canCreate" :title="problem">Create Team</button>
      </footer>
    </form>
  </section>
</template>
<style scoped>
.team-form {
  max-width: 720px;
  margin: 8px auto 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  font-size: 13px;
}
h1 {
  font-size: 25px;
  margin: 12px 0 0;
}
h2 {
  margin: 20px 0 0;
  font-size: 13px;
  font-weight: 500;
}
input,
textarea,
select {
  padding: 8px 10px;
  border: 1px solid var(--border);
  background: var(--input);
  border-radius: 7px;
  font-size: 13px;
}
input,
textarea {
  width: 100%;
}
textarea {
  min-height: 60px;
  resize: vertical;
}
.row {
  display: flex;
  gap: 8px;
  align-items: center;
}
.row > input,
.row > select {
  flex: 1;
}
.slot {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px;
  border: 1px solid var(--border);
  border-radius: 8px;
}
.slot > small,
.muted {
  color: var(--muted);
}
.chips {
  flex-wrap: wrap;
}
.chips > select {
  flex: 0 0 auto;
}
.chip {
  padding: 1px 8px;
}
.connector > .chip-text {
  flex: 1;
}
.criteria {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.core-criterion {
  color: var(--muted);
  display: flex;
  align-items: center;
  gap: 6px;
}
.steps {
  display: flex;
  align-items: center;
  gap: 10px;
}
.steps input {
  width: 80px;
}
footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin: 16px 0 32px;
}
.inline-error {
  margin: 0;
}
</style>
