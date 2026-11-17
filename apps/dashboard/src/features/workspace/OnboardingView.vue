<script setup lang="ts">
import { computed, inject, nextTick, onUnmounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useQueryClient } from '@tanstack/vue-query';
import Aristotle from '../coresearch/Aristotle.vue';
import Icon from '../../ui/Icon.vue';
import CliIcon from '../coresearch/CliIcon.vue';
import CapabilityList from './settings/library/CapabilityList.vue';
import { useSkillLibrary, useConnectorLibrary, useWorkspaceMCPServers } from '../../api/library';
import { backendLabel } from '../../lib/backends';
import { api } from '../../api/client';
import { daemonSessionKey } from '../../lib/daemon-session';
import { keys } from '../../api/keys';
import { workspaceId } from '../../api/session';
import { notify } from '../../state/notice';
import { useLaboratories, useMe, useRuntimes, useWorkspaces } from '../../api/queries';
import type { Workspace } from '../../api/types';
import LabChartScene from '../lab-chart/LabChartScene.vue';
import { toPreviewChart } from '../lab-chart/preview';
import { prefixFromSlug, randomWorkspaceName, slugFrom, stageIntro } from '../../lib/studies';
import { useWorkspaceTabs } from '../../state/workspace-tabs';

const desktopSession = inject(daemonSessionKey, null);
const route = useRoute();
const router = useRouter();
const client = useQueryClient();
const workspaces = useWorkspaces();
const runtimes = useRuntimes();
const laboratories = useLaboratories();
const me = useMe();
// The laboratory a new workspace starts with; the built-in one until another
// is chosen.
const laboratory = ref('built-in');
const previewChart = computed(() => {
  const lab = laboratories.data.value?.find((l) => l.id === laboratory.value);
  return lab && me.data.value
    ? toPreviewChart(lab, { user_id: me.data.value.id, name: me.data.value.name })
    : null;
});
const steps = ['Welcome', 'Laboratory', 'Skills', 'Connectors', 'Machines'] as const;
type Step = (typeof steps)[number];
const laboratoryQuery = (value: unknown) => value === 'laboratory' || value === 'workspace';
const step = ref<Step>(laboratoryQuery(route.query.step) ? 'Laboratory' : 'Welcome');
watch(
  () => route.query.step,
  (value) => {
    if (laboratoryQuery(value)) step.value = 'Laboratory';
  },
);
const position = computed(() => steps.indexOf(step.value));
const busy = ref(false);
const error = ref('');
const skills = useSkillLibrary();
const connectors = useConnectorLibrary();
const customConnectors = useWorkspaceMCPServers();
let visit = 0;
onUnmounted(() => visit++);
watch(workspaceId, () => visit++, { flush: 'sync' });

// Workspace step: an existing workspace, or a new one.
const choice = ref<string>('new');
watch(
  () => workspaces.data.value,
  (list) => {
    if (list?.length && choice.value === 'new' && step.value !== 'Laboratory')
      choice.value = list[0]!.id;
  },
  { immediate: true },
);
const name = ref('');
const slug = ref('');
const slugEdited = ref(false);
const prefix = ref('');
const pendingPrefix = ref<{ id: string; value: string }>();
watch(name, (n) => {
  if (!slugEdited.value) slug.value = slugFrom(n);
});
const prefixError = computed(() =>
  prefix.value && !/^[A-Z]{2,6}$/.test(prefix.value) ? 'Two to six capital letters.' : '',
);
const canCreate = computed(
  () => name.value.trim() && /^[a-z0-9][a-z0-9-]{1,38}$/.test(slug.value) && !prefixError.value,
);
const onlineRuntimes = computed(() =>
  (runtimes.data.value ?? []).filter((r) => r.status === 'online'),
);

async function run(fn: (adoptVisit: () => void) => Promise<void>) {
  if (busy.value) return;
  let currentVisit = visit;
  busy.value = true;
  error.value = '';
  try {
    await fn(() => {
      currentVisit = visit;
    });
  } catch (e) {
    if (visit === currentVisit) error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}

const saveWorkspace = () =>
  run(async (adoptVisit) => {
    let id = choice.value;
    let selected = choice.value;
    const cardPrefix = prefix.value;
    const patch: Record<string, string> = {};
    let currentVisit = visit;
    let workspace = workspaceId.value;
    const current = () =>
      visit === currentVisit && workspaceId.value === workspace && choice.value === selected;
    if (id === 'new') {
      const ws = await api<Workspace>('POST', '/api/workspaces', {
        name: name.value.trim(),
        slug: slug.value,
        laboratory: laboratory.value,
      });
      if (!current()) return;
      id = ws.id;
      selected = id;
      choice.value = id;
      workspaceId.value = id;
      workspace = id;
      currentVisit = visit;
      adoptVisit();
      if (cardPrefix) pendingPrefix.value = { id, value: cardPrefix };
      await client.invalidateQueries({ queryKey: keys.workspaces });
    }
    if (!current()) return;
    if (pendingPrefix.value?.id === id) patch.card_prefix = pendingPrefix.value.value;
    if (Object.keys(patch).length)
      await api('PATCH', `/api/workspaces/${id}`, patch, undefined, { workspace });
    if (!current()) return;
    if (pendingPrefix.value?.id === id) pendingPrefix.value = undefined;
    workspaceId.value = id;
    const selectedVisit = visit;
    await client.invalidateQueries({ queryKey: keys.workspaces });
    if (visit === selectedVisit && workspaceId.value === id) step.value = 'Skills';
  });

const back = () => {
  if (!busy.value) step.value = steps[Math.max(0, position.value - 1)]!;
};

const finish = () =>
  run(async () => {
    const currentVisit = visit;
    await api('PATCH', '/api/me', { onboarded: true });
    if (visit !== currentVisit) return;
    await client.invalidateQueries({ queryKey: keys.me });
    if (visit === currentVisit) await router.replace('/studies');
  });

async function logOut() {
  visit++;
  try {
    await desktopSession?.clear();
  } catch (e) {
    error.value = (e as Error).message;
    return;
  }
  try {
    const result = await api<{ localOnly?: boolean } | undefined>('POST', '/auth/logout');
    if (result?.localOnly)
      notify('Signed out on this computer. The server did not confirm sign-out.');
  } catch {
    error.value = 'Could not reach the server to log out. Try again.';
    return;
  }
  useWorkspaceTabs().clearAccount();
  await client.cancelQueries();
  workspaceId.value = '';
  await nextTick();
  await router.replace('/signin');
  await client.resetQueries();
}
</script>
<template>
  <div class="wizard">
    <aside class="rail">
      <div class="brand">
        <b>Λ</b>Lykeion<button
          class="icon-button push-right"
          :style="position > 0 ? undefined : { visibility: 'hidden' }"
          :disabled="busy || position === 0"
          aria-label="Back"
          @click="back"
        >
          <Icon name="back" />
        </button>
      </div>
      <ol class="steps">
        <li
          v-for="(s, i) in steps"
          :key="s"
          :class="{ done: i < position, current: i === position }"
          :aria-current="i === position ? 'step' : undefined"
        >
          <span class="dot"><Icon v-if="i < position" name="check" :size="10" /></span>{{ s }}
        </li>
      </ol>
      <button class="text-button" @click="logOut"><Icon name="logout" :size="14" />Log out</button>
    </aside>
    <main class="wizard-main">
      <header class="guide">
        <Aristotle still />
        <div><b>Aristotle</b><small>Your guide to scientific research</small></div>
      </header>
      <section
        class="content"
        :class="{ 'hide-scrollbar': step === 'Skills' || step === 'Connectors' }"
      >
        <template v-if="step === 'Welcome'">
          <h2 class="hero">A lab for your next discovery.</h2>
          <p class="sub">
            Aristotle is Lykeion's mascot and research coordinator. Let's set up your workspace: you
            set the questions, agents carry out the work.
          </p>
          <div v-for="(s, i) in stageIntro" :key="s.name" class="stage-line">
            <span class="num">{{ i + 1 }}</span>
            <div>
              <b>{{ s.name }}</b>
              <div class="muted">{{ s.line }}</div>
            </div>
          </div>
        </template>
        <template v-else-if="step === 'Laboratory'">
          <h2>
            {{
              workspaces.data.value?.length
                ? `Continue with ${workspaces.data.value[0]!.name}, or start another.`
                : 'Create your laboratory.'
            }}
          </h2>
          <p class="sub">You can belong to any number of workspaces.</p>
          <label v-for="w in workspaces.data.value ?? []" :key="w.id" class="choice">
            <span class="workspace-avatar">{{ w.name[0] }}</span>
            <span
              ><b>{{ w.name }}</b
              ><small class="mono">{{ w.slug }}</small></span
            >
            <input
              v-model="choice"
              type="radio"
              name="workspace"
              :value="w.id"
              :aria-label="w.name"
            />
          </label>
          <div class="choice" :class="{ selected: choice === 'new' }">
            <label class="choice-head">
              <span class="workspace-avatar">+</span><b>Create a new workspace</b>
              <input
                v-model="choice"
                type="radio"
                name="workspace"
                value="new"
                aria-label="Create a new workspace"
              />
            </label>
            <template v-if="choice === 'new'">
              <label class="field"
                >Workspace name
                <span class="row"
                  ><input
                    v-model="name"
                    aria-label="Workspace name"
                    placeholder="Acme Inc, My Lab, Side Projects…"
                  /><button
                    type="button"
                    class="button compact"
                    @click="name = randomWorkspaceName()"
                  >
                    <Icon name="refresh" :size="13" />Random
                  </button></span
                ></label
              >
              <label class="field"
                >URL<input v-model="slug" class="mono" aria-label="URL" @input="slugEdited = true"
              /></label>
              <label class="field"
                >Card prefix<input
                  v-model="prefix"
                  aria-label="Card prefix"
                  maxlength="6"
                  :placeholder="prefixFromSlug(slug)"
                  @input="prefix = prefix.toUpperCase()"
              /></label>
              <small class="muted"
                >Study numbers follow the URL, like {{ prefix || prefixFromSlug(slug) }}-12, unless
                you type a prefix here.</small
              >
              <small v-if="prefixError" class="field-error">{{ prefixError }}</small>
              <label v-for="l in laboratories.data.value ?? []" :key="l.id" class="choice-head">
                <span class="workspace-avatar">{{ l.name[0] }}</span><b>{{ l.name }}</b>
                <input
                  v-model="laboratory"
                  type="radio"
                  name="laboratory"
                  :value="l.id"
                  :aria-label="l.name"
                />
              </label>
              <LabChartScene
                v-if="previewChart"
                :chart="previewChart"
                preview
                height="420px"
                :me="me.data.value?.id"
              />
            </template>
          </div>
        </template>
        <template v-else-if="step === 'Machines'">
          <h2>Machines</h2>
          <p class="sub">Connect a computer so agents can run research.</p>
          <p class="muted machine-hint">
            Not connected yet? Run <code>lyk login</code> then <code>lyk daemon start</code>.
          </p>
          <div class="runtime-box">
            <b>{{ runtimes.data.value?.length ?? 0 }} agent runtimes</b>
            <span v-if="runtimes.data.value?.length">· {{ onlineRuntimes.length }} online</span
            ><button class="text-button push-right" @click="runtimes.refetch()">
              <Icon name="refresh" :size="13" />Refresh
            </button>
          </div>
          <div v-for="r in runtimes.data.value ?? []" :key="r.id" class="runtime-row">
            <span
              ><CliIcon
                v-if="r.harness === 'claude' || r.harness === 'codex'"
                :cli="r.harness"
              /><template v-else>{{ r.harness[0] }}</template></span
            >
            <div>
              {{ backendLabel(r.harness)
              }}<small
                >{{ r.custom_name || r.device_name
                }}<template v-if="r.cli_version"> · {{ r.cli_version }}</template></small
              >
            </div>
            <span class="push-right" :class="r.status">●</span>
          </div>
        </template>
        <template v-else-if="step === 'Skills'">
          <h2>Skills</h2>
          <p class="sub">Instruction packs agents can use while working on your studies.</p>
          <CapabilityList
            :query="''"
            :loading="skills.isPending.value"
            :error="skills.error.value?.message"
            :count="skills.data.value?.length ?? 0"
            @retry="skills.refetch()"
          >
            <article
              v-for="skill in skills.data.value ?? []"
              :key="skill.id"
              class="onboarding-card"
            >
              <b>{{ skill.name }}</b>
              <p>{{ skill.description }}</p>
            </article>
          </CapabilityList>
        </template>
        <template v-else-if="step === 'Connectors'">
          <h2>Connectors</h2>
          <p class="sub">Connections agents can use to reach scientific databases and tools.</p>
          <CapabilityList
            :query="''"
            noun="connectors"
            :loading="connectors.isPending.value || customConnectors.isPending.value"
            :error="connectors.error.value?.message || customConnectors.error.value?.message"
            :count="
              (connectors.data.value?.length ?? 0) + (customConnectors.data.value?.length ?? 0)
            "
            @retry="
              connectors.refetch();
              customConnectors.refetch();
            "
          >
            <article
              v-for="connector in connectors.data.value ?? []"
              :key="connector.name"
              class="onboarding-card"
            >
              <div>
                <b>{{ connector.name }}</b>
                <p>{{ connector.purpose }}</p>
                <small v-if="!connector.enabled">{{ connector.reason }}</small>
              </div>
            </article>
            <article
              v-for="connector in customConnectors.data.value ?? []"
              :key="connector.id"
              class="onboarding-card"
            >
              <div>
                <b>{{ connector.name }}</b>
                <p>Custom · {{ connector.transport === 'url' ? 'Remote URL' : 'Command' }}</p>
              </div>
            </article>
          </CapabilityList>
        </template>
        <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
      </section>
      <footer class="actions">
        <template v-if="step === 'Welcome'">
          <button class="button primary" @click="step = 'Laboratory'">Set up your lab</button>
          <button class="text-button" :disabled="busy" @click="finish">
            I've done this before
          </button>
        </template>
        <template v-else-if="step === 'Laboratory'">
          <button
            class="button primary"
            :disabled="busy || (choice === 'new' && !canCreate)"
            @click="saveWorkspace"
          >
            {{ choice === 'new' ? 'Create workspace' : 'Continue' }}
          </button>
        </template>
        <template v-else-if="step === 'Skills' || step === 'Connectors'">
          <button
            class="button primary"
            @click="step = step === 'Skills' ? 'Connectors' : 'Machines'"
          >
            Continue
          </button>
          <button class="text-button" @click="step = step === 'Skills' ? 'Connectors' : 'Machines'">
            Skip
          </button>
        </template>
        <button v-else class="button primary" :disabled="busy" @click="finish">
          Enter workspace
        </button>
      </footer>
    </main>
  </div>
</template>
<style scoped>
.machine-hint code {
  padding: 2px 5px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--canvas);
}
.wizard {
  display: flex;
  gap: 14px;
  height: 100dvh;
  overflow: hidden;
  padding: 14px;
  background: var(--surface);
}
.rail {
  width: 290px;
  flex: none;
  background: var(--canvas);
  border-radius: 14px;
  padding: 22px 20px;
  display: flex;
  flex-direction: column;
}
.brand {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 500;
}
.brand b {
  color: var(--primary-text);
}
.steps {
  margin: auto 0;
  list-style: none;
  padding: 0;
}
.steps li {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 9px 0;
  color: var(--muted);
  font-size: 13.5px;
}
.steps li.done,
.steps li.current {
  color: var(--text);
}
.steps li.current {
  font-weight: 500;
}
.dot {
  width: 16px;
  height: 16px;
  border-radius: 50%;
  border: 1.5px solid var(--border);
  display: grid;
  place-items: center;
  color: var(--on-primary);
}
.done .dot {
  background: var(--primary);
  border-color: var(--primary);
}
.current .dot {
  border: 5px solid var(--primary);
}
.rail > .text-button {
  display: flex;
  align-items: center;
  gap: 6px;
}
.wizard-main {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.guide {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: 16px;
  padding: 8px 0 16px;
  border-bottom: 1px solid var(--border);
}
.guide :deep(.aristotle-art) {
  width: 48px;
  height: 62px;
}
.guide b {
  display: block;
  font-weight: 500;
}
.guide small {
  color: var(--muted);
  font-size: 12.5px;
}
.content {
  flex: 1;
  width: min(100%, 760px);
  margin: 0 auto;
  min-height: 0;
  overflow: auto;
  padding: 32px 24px 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.content h2 {
  font-size: 19px;
  font-weight: 600;
}
.content .hero {
  font-size: 34px;
  line-height: 1.15;
  white-space: nowrap;
}
.centered {
  text-align: center;
}
.sub {
  color: var(--muted);
  font-size: 13.5px;
  margin-bottom: 12px;
}
.stage-line {
  width: 100%;
  min-width: 0;
  display: flex;
  gap: 14px;
  font-size: 13.5px;
}
.stage-line > div {
  min-width: 0;
  overflow-wrap: anywhere;
}
.stage-line b {
  font-weight: 500;
}
.num {
  width: 20px;
  height: 20px;
  border-radius: 50%;
  border: 1px solid var(--border);
  display: grid;
  place-items: center;
  font-size: 10px;
  flex: none;
}
.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13.5px;
  font-weight: 500;
}
.field input,
.field textarea,
.field select {
  font: inherit;
  font-weight: 400;
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 7px 10px;
  background: var(--surface);
}
.row {
  display: flex;
  gap: 8px;
}
.row input {
  flex: 1;
}
.choice {
  background: var(--input);
  border-radius: 10px;
  padding: 14px 18px;
  display: flex;
  align-items: center;
  gap: 14px;
  font-size: 13.5px;
}
.choice.selected,
.choice.block {
  display: flex;
  flex-direction: column;
  align-items: stretch;
}
.choice.selected {
  box-shadow: inset 0 0 0 1.5px var(--text);
}
.choice-head {
  display: flex;
  align-items: center;
  gap: 12px;
}
.choice input[type='radio'] {
  margin-left: auto;
}
.choice small {
  display: block;
  color: var(--muted);
}
.mono {
  font-family: 'JetBrains Mono', ui-monospace, monospace;
}
.field-error {
  color: var(--error-text);
}
.runtime-box {
  background: var(--input);
  border-radius: 8px;
  padding: 12px 16px;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12.5px;
}
.runtime-box .text-button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.runtime-row {
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 9px 12px;
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13.5px;
}
.runtime-row small {
  display: block;
  color: var(--muted);
}
.runtime-row .online {
  color: var(--stage-validation-dot);
}
.runtime-row .offline {
  color: var(--muted);
}
.actions {
  border-top: 1px solid var(--border);
  padding: 14px 0 8px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}
.actions .button.primary {
  width: min(100% - 32px, 712px);
  justify-content: center;
}
.content .inline-error {
  margin: 0;
}
.content :deep(.library-filters),
.content :deep(.library-caption) {
  display: none;
}
.onboarding-card {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
  border: 1px solid var(--border);
  border-radius: 7px;
  padding: 12px;
  margin-bottom: 7px;
  font-size: 13px;
}
.onboarding-card:has(> b) {
  display: block;
}
.onboarding-card b {
  font-weight: 500;
}
.onboarding-card p {
  font-size: 13px;
  color: var(--muted);
  line-height: 1.5;
  margin: 4px 0 0;
}
.onboarding-card small {
  display: block;
  font-size: 13px;
  color: var(--muted);
  margin-top: 6px;
}
@media (max-width: 900px) {
  .rail {
    width: 220px;
  }
}
@media (max-width: 600px) {
  .wizard {
    padding: 8px;
    gap: 8px;
    flex-direction: column;
  }
  .rail {
    width: 100%;
    padding: 12px;
  }
  .brand {
    padding-right: 90px;
  }
  .steps {
    display: flex;
    gap: 4px 10px;
    flex-wrap: wrap;
    margin: 10px 0 0;
  }
  .steps li {
    font-size: 13px;
    gap: 5px;
    flex: none;
  }
  .dot {
    width: 12px;
    height: 12px;
  }
  .rail > .text-button {
    position: absolute;
    right: 24px;
    top: 20px;
  }
  .wizard-main {
    min-height: 0;
  }
  .guide {
    padding: 4px 0 10px;
  }
  .content {
    padding: 20px 12px 12px;
  }
  .content .hero {
    font-size: min(28px, 5.8vw);
  }
  .actions {
    flex: none;
  }
  .row input {
    min-width: 0;
  }
  .choice {
    padding: 12px;
  }
}
</style>
