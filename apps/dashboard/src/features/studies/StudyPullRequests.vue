<script setup lang="ts">
import { nextTick, ref } from 'vue';
import { useGitHubMutations, useStudyPullRequests } from '../../api/library';
import type { StudyPullRequest } from '../../api/types';
import Icon from '../../ui/Icon.vue';

const props = defineProps<{ cardId: string }>();
const prs = useStudyPullRequests(() => props.cardId);
const { linkPullRequest, unlinkPullRequest } = useGitHubMutations();
const formOpen = ref(false);
const menu = ref('');
const url = ref('');
const input = ref<HTMLInputElement>();
const addButton = ref<HTMLButtonElement>();
const busy = ref(false);
const error = ref('');
const stateLabels: Record<string, string> = {
  open: 'Open',
  draft: 'Draft',
  merged: 'Merged',
  closed: 'Closed',
};
const chipClasses: Record<string, string> = {
  open: 'chip-ready',
  draft: 'chip-queued',
  merged: 'chip-in_review',
  closed: 'chip-blocked',
};
const mergeLabels: Record<string, string> = {
  conflicting: 'Conflicts',
  blocked: 'Blocked',
  behind: 'Behind',
};
function ci(pr: StudyPullRequest) {
  switch (pr.ci_state) {
    case 'passed':
      return typeof pr.ci_passed === 'number'
        ? `All checks passed (${pr.ci_passed})`
        : 'All checks passed';
    case 'failed':
      return pr.ci_failing?.length ? `Failed: ${pr.ci_failing.join(', ')}` : 'Checks failed';
    case 'pending':
      return 'Checks running';
    default:
      return '';
  }
}
function leaveActions(event: FocusEvent) {
  if (!(event.currentTarget as HTMLElement).contains(event.relatedTarget as Node | null))
    menu.value = '';
}
function closeActions(event: KeyboardEvent) {
  menu.value = '';
  (event.currentTarget as HTMLElement).querySelector('button')?.focus();
}
async function toggleForm() {
  if (busy.value) return;
  formOpen.value = !formOpen.value;
  menu.value = '';
  error.value = '';
  url.value = '';
  await nextTick();
  (formOpen.value ? input.value : addButton.value)?.focus();
}
async function save(id?: string) {
  if (busy.value || (!id && !url.value.trim())) return;
  busy.value = true;
  error.value = '';
  try {
    if (id) await unlinkPullRequest(props.cardId, id);
    else await linkPullRequest(props.cardId, url.value.trim());
    formOpen.value = false;
    menu.value = '';
    url.value = '';
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
  if (!error.value) {
    await nextTick();
    addButton.value?.focus();
  }
}
</script>
<template>
  <section class="pull-requests" aria-label="Pull requests">
    <header>
      <h2>Pull requests</h2>
      <button
        ref="addButton"
        class="icon-button"
        aria-label="Add pull request"
        title="Add pull request"
        :aria-expanded="formOpen"
        :disabled="busy || !prs.isSuccess.value"
        @click="toggleForm"
      >
        <Icon name="plus" :size="16" />
      </button>
    </header>
    <form v-if="formOpen" @submit.prevent="save()" @keydown.esc.prevent="toggleForm">
      <label class="type-label"
        >Pull request link
        <input
          ref="input"
          v-model="url"
          aria-label="Pull request link"
          type="url"
          required
          placeholder="https://github.com/…"
          :disabled="busy"
        />
      </label>
      <div class="form-actions">
        <button type="button" class="text-button" :disabled="busy" @click="toggleForm">
          Cancel
        </button>
        <button type="submit" class="button" :disabled="busy || !url.trim()">
          Link pull request
        </button>
      </div>
    </form>
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
    <p v-if="prs.isError.value" class="inline-error" role="alert">
      Could not load pull requests: {{ prs.error.value?.message }}
      <button class="text-button" @click="prs.refetch()">Retry</button>
    </p>
    <p v-else-if="prs.isPending.value" class="empty" role="status">Loading pull requests…</p>
    <p v-else-if="!prs.data.value?.length" class="empty">No pull requests yet.</p>
    <ul v-if="prs.data.value?.length">
      <li v-for="pr in prs.data.value" :key="pr.id">
        <a
          :href="pr.html_url?.startsWith('https://github.com/') ? pr.html_url : undefined"
          target="_blank"
          rel="noopener noreferrer"
        >
          <span class="type-label repo"
            >{{ pr.repo_owner }}/{{ pr.repo_name }} #{{ pr.number }}</span
          >
          <strong>{{ pr.title }}</strong>
          <span class="byline"
            ><span class="chip" :class="chipClasses[pr.state]">{{
              stateLabels[pr.state] ?? pr.state
            }}</span
            >{{ pr.author_login }}</span
          >
          <span class="size">
            <span>{{ pr.changed_files }} {{ pr.changed_files === 1 ? 'file' : 'files' }} changed,</span>
            <span>
              <span class="insertions"
                >+{{ pr.additions }} {{ pr.additions === 1 ? 'insertion' : 'insertions' }}(+)</span
              >,
            </span>
            <span class="deletions">
              -{{ pr.deletions }} {{ pr.deletions === 1 ? 'deletion' : 'deletions' }}(-)
            </span>
          </span>
          <template v-if="pr.state === 'open' || pr.state === 'draft'">
            <span v-if="ci(pr)">{{ ci(pr) }}{{ pr.snapshot_stale ? ' (stale)' : '' }}</span>
            <span v-if="mergeLabels[pr.mergeability ?? '']"
              >{{ mergeLabels[pr.mergeability!] }}{{ pr.snapshot_stale ? ' (stale)' : '' }}</span
            >
          </template>
        </a>
        <div class="row-actions" @focusout="leaveActions" @keydown.esc.stop="closeActions">
          <button
            class="icon-button"
            :aria-label="`Pull request actions for ${pr.repo_owner}/${pr.repo_name} #${pr.number}`"
            :aria-expanded="menu === pr.id"
            :disabled="busy"
            @click="menu = menu === pr.id ? '' : pr.id"
          >
            <Icon name="more" :size="16" />
          </button>
          <button v-if="menu === pr.id" class="button remove" :disabled="busy" @click="save(pr.id)">
            Remove from study
          </button>
        </div>
      </li>
    </ul>
  </section>
</template>
<style scoped>
.pull-requests {
  min-width: 0;
  padding: 24px 20px;
}
header,
.byline,
.form-actions {
  display: flex;
  gap: 8px;
  align-items: center;
}
header {
  justify-content: space-between;
  margin-bottom: 16px;
}
h2 {
  font-size: 16px;
  margin: 0;
}
.empty,
a {
  color: var(--muted);
}
.empty {
  font-size: 13px;
}
ul {
  list-style: none;
  padding: 0;
  margin: 0;
}
li {
  position: relative;
  border-top: 1px solid var(--border);
}
li a {
  display: grid;
  gap: 4px;
  padding: 12px 0;
  font-size: 12px;
  overflow-wrap: anywhere;
  text-decoration: none;
}
.repo {
  padding-right: 30px;
}
strong {
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
}
li a:hover strong,
li a:focus-visible strong {
  text-decoration: underline;
}
.byline {
  flex-wrap: wrap;
}
.size {
  display: flex;
  flex-wrap: wrap;
  column-gap: 4px;
  font-variant-numeric: tabular-nums;
}
.insertions {
  color: color-mix(in srgb, var(--stage-validation-dot) 55%, var(--text));
}
.deletions {
  color: var(--danger-text);
}
.row-actions {
  position: absolute;
  top: 4px;
  right: 0;
}
.remove {
  position: absolute;
  top: 100%;
  right: 0;
  background: var(--surface);
  white-space: nowrap;
  z-index: 1;
}
form {
  padding: 0 0 12px;
}
input {
  display: block;
  width: 100%;
  min-width: 0;
  box-sizing: border-box;
  margin-top: 4px;
}
.form-actions {
  justify-content: flex-end;
  flex-wrap: wrap;
  margin-top: 8px;
}
</style>
