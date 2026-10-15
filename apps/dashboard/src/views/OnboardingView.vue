<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useQueryClient } from '@tanstack/vue-query';
import Aristotle from '../components/Aristotle.vue';
import Icon from '../components/Icon.vue';
import { api } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import { useMe, useRuntimes, useWorkspaces } from '../api/queries';
import { careerStages, type CareerStage, type Workspace } from '../api/types';
import {
  careerStageLabels,
  prefixFromSlug,
  randomWorkspaceName,
  slugFrom,
  stageIntro,
} from '../lib/studies';

const route = useRoute();
const router = useRouter();
const client = useQueryClient();
const me = useMe();
const workspaces = useWorkspaces();
const runtimes = useRuntimes();
const steps = ['Welcome', 'Research profile', 'Workspace', 'Runtime', 'Capabilities', 'Ready'];
const step = ref(route.query.step === 'workspace' ? 2 : 0);
const busy = ref(false);
const error = ref('');

const context = ref('');
const career = ref<CareerStage>('');
watch(
  () => me.data.value,
  (m) => {
    if (m && !career.value) career.value = m.career_stage;
  },
  { immediate: true },
);

// Workspace step: an existing workspace, or a new one.
const choice = ref<string>('new');
watch(
  () => workspaces.data.value,
  (list) => {
    if (list?.length && choice.value === 'new' && step.value !== 2) choice.value = list[0]!.id;
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
const chosen = computed<Workspace | undefined>(() =>
  workspaces.data.value?.find((w) => w.id === workspaceId.value),
);
const onlineRuntimes = computed(() =>
  (runtimes.data.value ?? []).filter((r) => r.status === 'online'),
);

async function run(fn: () => Promise<void>) {
  busy.value = true;
  error.value = '';
  try {
    await fn();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}

const saveProfile = () =>
  run(async () => {
    if (career.value !== (me.data.value?.career_stage ?? ''))
      await api('PATCH', '/api/me', { career_stage: career.value });
    await client.invalidateQueries({ queryKey: keys.me });
    step.value = 2;
  });

const saveWorkspace = () =>
  run(async () => {
    let id = choice.value;
    const patch: Record<string, string> = {};
    if (context.value.trim()) patch.research_context = context.value.trim();
    if (id === 'new') {
      const ws = await api<Workspace>('POST', '/api/workspaces', {
        name: name.value.trim(),
        slug: slug.value,
      });
      id = ws.id;
      choice.value = id;
      if (prefix.value) pendingPrefix.value = { id, value: prefix.value };
      await client.invalidateQueries({ queryKey: keys.workspaces });
    }
    if (pendingPrefix.value?.id === id) patch.card_prefix = pendingPrefix.value.value;
    if (Object.keys(patch).length) await api('PATCH', `/api/workspaces/${id}`, patch);
    if (pendingPrefix.value?.id === id) pendingPrefix.value = undefined;
    workspaceId.value = id;
    await client.invalidateQueries({ queryKey: keys.workspaces });
    step.value = 3;
  });

const finish = () =>
  run(async () => {
    await api('PATCH', '/api/me', { onboarded: true });
    await client.invalidateQueries({ queryKey: keys.me });
    await router.replace('/studies');
  });

async function logOut() {
  try {
    await api('POST', '/auth/logout');
  } catch {
    error.value = 'Could not reach the server to log out. Try again.';
    return;
  }
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
          v-if="step > 0"
          class="icon-button push-right"
          aria-label="Back"
          @click="step--"
        >
          <Icon name="back" />
        </button>
      </div>
      <ol class="steps">
        <li
          v-for="(s, i) in steps"
          :key="s"
          :class="{ done: i < step, current: i === step }"
          :aria-current="i === step ? 'step' : undefined"
        >
          <span class="dot"><Icon v-if="i < step" name="check" :size="10" /></span>{{ s }}
        </li>
      </ol>
      <button class="text-button" @click="logOut"><Icon name="logout" :size="14" />Log out</button>
    </aside>
    <main class="wizard-main">
      <header class="guide">
        <Aristotle still />
        <div><b>Aristotle</b><span>Your guide to scientific research</span></div>
      </header>
      <section class="content">
        <template v-if="step === 0">
          <small class="muted">Welcome to Lykeion</small>
          <h2 class="hero">A lab for your<br />next discovery.</h2>
          <p class="sub">
            Aristotle is Lykeion's mascot and research coordinator. Let's set up your workspace: you
            set the questions, agents carry out the work, and gates decide when it moves on.
          </p>
          <div v-for="(s, i) in stageIntro" :key="s.name" class="stage-line">
            <span class="num">{{ i + 1 }}</span>
            <div>
              <b>{{ s.name }}</b>
              <div class="muted">{{ s.line }}</div>
            </div>
          </div>
        </template>
        <template v-else-if="step === 1">
          <h2 class="centered">What is your research about?</h2>
          <p class="sub centered">
            Describe your field, methods and current project. Every agent in this workspace reads it
            on every run.
          </p>
          <label class="field"
            >Research context<textarea
              v-model="context"
              aria-label="Research context"
              rows="6"
              placeholder="e.g. I study how single-cell RNA sequencing can reveal mechanisms of zebrafish heart regeneration, using Seurat and Nextflow."
            />
          </label>
          <label class="field"
            >Your career stage<select v-model="career" aria-label="Your career stage">
              <option value="">Choose a stage (optional)</option>
              <option v-for="c in careerStages" :key="c" :value="c">
                {{ careerStageLabels[c] }}
              </option>
            </select></label
          >
        </template>
        <template v-else-if="step === 2">
          <h2>
            {{
              workspaces.data.value?.length
                ? `Continue with ${workspaces.data.value[0]!.name}, or start another.`
                : 'Create your workspace.'
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
            </template>
          </div>
        </template>
        <template v-else-if="step === 3">
          <h2>Connect a research runtime</h2>
          <p class="sub">Connect a computer so agents can run research.</p>
          <p class="muted">
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
            <span>{{ r.harness === 'claude' ? '✳' : '◎' }}</span>
            <div>
              {{ r.harness === 'claude' ? 'Claude Code' : 'Codex'
              }}<small
                >{{ r.custom_name || r.device_name
                }}<template v-if="r.cli_version"> · {{ r.cli_version }}</template></small
              >
            </div>
            <span class="push-right" :class="r.status">●</span>
          </div>
        </template>
        <template v-else-if="step === 4">
          <h2>Discover your lab's capabilities</h2>
          <p class="sub">Sources are where agents find evidence. Every claim cites one of them.</p>
          <div class="choice block">
            <b>Sources</b>
            <div class="muted">Europe PMC (full texts), OpenAlex (literature), GEO (datasets).</div>
          </div>
          <div class="choice block">
            <b>Skills and connectors</b>
            <div class="muted">None configured yet. You can add them later.</div>
          </div>
        </template>
        <template v-else>
          <h2>Your lab is ready</h2>
          <div class="summary">
            <small>Workspace</small>
            <div>{{ chosen?.name ?? 'None yet' }}</div>
            <small>Research context</small>
            <div>{{ chosen?.research_context || 'None' }}</div>
            <small>Runtimes</small>
            <div>{{ onlineRuntimes.length }} online of {{ runtimes.data.value?.length ?? 0 }}</div>
          </div>
        </template>
        <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
      </section>
      <footer class="actions">
        <template v-if="step === 0">
          <button class="button primary" @click="step = 1">Set up your lab</button>
          <button class="text-button" :disabled="busy" @click="finish">
            I've done this before
          </button>
        </template>
        <template v-else-if="step === 1">
          <small class="muted"
            >Both answers are optional. You can edit them later in Settings.</small
          >
          <button class="button primary" :disabled="busy" @click="saveProfile">Continue</button>
          <button class="text-button" @click="step = 2">Skip</button>
        </template>
        <template v-else-if="step === 2">
          <button
            class="button primary"
            :disabled="busy || (choice === 'new' && !canCreate)"
            @click="saveWorkspace"
          >
            {{ choice === 'new' ? 'Create workspace' : 'Continue' }}
          </button>
        </template>
        <template v-else-if="step === 3">
          <button class="button primary" @click="step = 4">Continue</button>
          <button class="text-button" @click="step = 4">Skip for now</button>
        </template>
        <template v-else-if="step === 4">
          <small class="muted"
            >This is a short introduction. You can configure capabilities later.</small
          >
          <button class="button primary" @click="step = 5">Continue</button>
          <button class="text-button" @click="step = 5">Skip</button>
        </template>
        <button v-else class="button primary" :disabled="busy" @click="finish">
          Enter workspace
        </button>
      </footer>
    </main>
  </div>
</template>
<style scoped>
.wizard {
  display: flex;
  gap: 14px;
  min-height: 100vh;
  padding: 14px;
  background: var(--surface);
}
.rail {
  width: 318px;
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
  color: var(--primary);
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
  color: #fff;
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
.guide :deep(.aristotle-art > span) {
  width: 129.6px;
  height: 181.2px;
  transform: translate(-31.2px, -7.2px);
}
.guide b {
  display: block;
  font-weight: 500;
}
.guide span {
  color: var(--muted);
  font-size: 12.5px;
}
.content {
  flex: 1;
  width: 440px;
  margin: 0 auto;
  padding-top: 40px;
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
  display: flex;
  gap: 14px;
  font-size: 13.5px;
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
.summary {
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 16px 20px;
  font-size: 13.5px;
  display: grid;
  gap: 4px;
}
.summary small {
  color: var(--muted);
  margin-top: 8px;
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
  width: 440px;
  justify-content: center;
}
.content .inline-error {
  margin: 0;
}
</style>
