<script setup lang="ts">
import { defineAsyncComponent, defineComponent, h, computed, onUnmounted, ref, watch } from 'vue';
import Modal from '../../../../ui/Modal.vue';
import Icon from '../../../../ui/Icon.vue';
import { useSkillDetail, useSkillFile, useLibraryMutations } from '../../../../api/library';
import { useMe, useMembers } from '../../../../api/queries';
import { workspaceId } from '../../../../api/session';
import { ApiError } from '../../../../api/client';
import { useResourceDraft } from '../../../../state/resource-draft';
import SkillFileViewer from './SkillFileViewer.vue';
const formattingFailed = ref(false);
const PlainRecordedText = defineComponent({
  props: { content: { type: String, default: '' } },
  setup: (text) => () => h('div', { class: 'recorded-text-fallback' }, text.content),
});
const RecordedMarkdown = defineAsyncComponent({
  loader: () => import('../../../../ui/RecordedMarkdown.vue'),
  loadingComponent: PlainRecordedText,
  errorComponent: PlainRecordedText,
  delay: 0,
  onError: (_error, _retry, fail) => {
    formattingFailed.value = true;
    fail();
  },
});

const props = defineProps<{ skillId: string; startEdit?: boolean }>();
const emit = defineEmits<{ close: [] }>();
const detail = useSkillDetail(() => props.skillId);
const mutations = useLibraryMutations();
const me = useMe(),
  members = useMembers();
const tab = ref<'overview' | 'files' | 'editing'>(props.startEdit ? 'editing' : 'overview');
const path = ref('SKILL.md'),
  preview = ref(''),
  error = ref(''),
  busy = ref(false),
  refused = ref(false),
  deleting = ref(false);
const file = useSkillFile(() => props.skillId, path);
const identity = computed(() => `${workspaceId.value}:${props.skillId}`);
const saved = computed(() =>
  !refused.value && !detail.isError.value && detail.data.value?.editable
    ? { revision: detail.data.value.revision, value: { content: detail.data.value.content } }
    : undefined,
);
const form = useResourceDraft(identity, saved);
let refusalSnapshot = detail.data.value;
watch(
  () => detail.data.value,
  (snapshot) => {
    if (
      refused.value &&
      snapshot &&
      snapshot !== refusalSnapshot &&
      snapshot.editable &&
      !detail.isError.value
    )
      refused.value = false;
  },
);

const instructions = computed(() =>
  (detail.data.value?.content ?? '').replace(/^---\r?\n[\s\S]*?\r?\n---(?:\r?\n|$)/, ''),
);
const tree = computed(() => {
  const rows: { path: string; label: string; depth: number; directory: boolean }[] = [];
  const directories = new Set<string>();
  for (const file of [...(detail.data.value?.files ?? [])].sort((a, b) =>
    a.path === 'SKILL.md' ? -1 : b.path === 'SKILL.md' ? 1 : a.path.localeCompare(b.path),
  )) {
    const parts = file.path.split('/');
    for (let depth = 0; depth < parts.length - 1; depth++) {
      const path = parts.slice(0, depth + 1).join('/');
      if (!directories.has(path)) {
        directories.add(path);
        rows.push({ path, label: parts[depth] + '/', depth, directory: true });
      }
    }
    rows.push({ path: file.path, label: parts.at(-1)!, depth: parts.length - 1, directory: false });
  }
  return rows;
});
const metadata = computed(() => detail.data.value?.files.find((f) => f.path === path.value));
const previewableBinary = computed(() => /\.(png|jpe?g|gif|webp|bmp|ico|svg|pdf)$/i.test(path.value));
const author = computed(() => {
  const s = detail.data.value;
  if (s?.origin === 'bundled') return 'Bundled by Lykeion';
  if (s?.created_by === me.data.value?.id) return 'You';
  const member = members.data.value?.find((m) => m.user_id === s?.created_by);
  return member?.name || member?.email || s?.created_by || 'Workspace member';
});
let visit = 0,
  previewVisit = 0;
onUnmounted(() => {
  visit++;
  previewVisit++;
});
watch([() => file.data.value, path], async ([blob]) => {
  preview.value = '';
  const own = ++previewVisit;
  const text = blob && !metadata.value?.binary ? await blob.text() : '';
  if (own === previewVisit) preview.value = text;
});
async function save() {
  if (!form.canSave.value || busy.value) return;
  const accept = form.adopt(),
    own = identity.value,
    token = visit,
    revision = form.baseRevision.value!,
    content = form.draft.value!.content;
  busy.value = true;
  error.value = '';
  try {
    const response = await mutations.updateSkill(props.skillId, { revision, content });
    if (
      own === identity.value &&
      token === visit &&
      accept({ revision: response.revision, value: { content: response.content } })
    )
      if (!form.dirty.value) tab.value = 'overview';
  } catch (e) {
    if (own !== identity.value || token !== visit) return;
    error.value = (e as Error).message;
    if (e instanceof ApiError && (e.status === 409 || e.status === 403 || e.status === 404)) {
      refusalSnapshot = detail.data.value;
      refused.value = true;
      const refreshed = await detail.refetch();
      if (
        own === identity.value &&
        token === visit &&
        !refreshed.isError &&
        refreshed.data?.editable
      )
        refused.value = false;
    }
  } finally {
    if (own === identity.value && token === visit) busy.value = false;
  }
}
async function remove(close: () => Promise<void>) {
  if (!saved.value || !detail.data.value?.editable || busy.value) return;
  if (!(await form.reset())) return;
  const own = identity.value;
  busy.value = true;
  error.value = '';
  try {
    await mutations.deleteSkill(props.skillId, detail.data.value.revision);
    if (own === identity.value) await close();
  } catch (e) {
    if (own === identity.value) {
      error.value = (e as Error).message;
      deleting.value = false;
      await detail.refetch();
    }
  } finally {
    if (own === identity.value) busy.value = false;
  }
}
async function cancelEdit() {
  if (await form.reset()) tab.value = 'overview';
}
</script>
<template>
  <Modal
    v-slot="{ close }"
    class="skill-modal"
    :title="detail.data.value?.name || 'Skill'"
    :before-close="form.reset"
    @close="emit('close')"
  >
    <section class="skill-detail">
      <header class="detail-head">
        <div class="crumb">Skills / {{ detail.data.value?.name || 'Loading…' }}</div>
        <div class="detail-title">
          <h2>{{ detail.data.value?.name || 'Skill' }}</h2>
          <button
            v-if="detail.data.value?.editable && tab !== 'editing'"
            class="icon-button"
            aria-label="Edit"
            :disabled="!saved || busy"
            @click="tab = 'editing'"
          >
            <Icon name="edit" :size="16" /></button
          ><button
            v-if="detail.data.value?.editable"
            class="icon-button"
            aria-label="Delete skill"
            :disabled="!saved || busy"
            @click="deleting = true"
          >
            <Icon name="trash" :size="16" /></button
          ><button class="icon-button" aria-label="Close skill" @click="close">
            <Icon name="close" :size="18" />
          </button>
        </div>
        <p class="lede">{{ detail.data.value?.description }}</p>
        <div v-if="detail.data.value" class="chips">
          <span>{{
            detail.data.value.origin === 'bundled' ? 'K-Dense · Read-only' : 'Workspace skill'
          }}</span
          ><span v-if="detail.data.value.contains_scripts" class="script-chip"
            >Contains scripts</span
          >
        </div>
      </header>
      <nav class="detail-tabs" role="tablist" aria-label="Skill details">
        <button role="tab" :aria-selected="tab === 'overview'" @click="tab = 'overview'">
          Overview</button
        ><button role="tab" :aria-selected="tab !== 'overview'" @click="tab = 'files'">
          Files
        </button>
      </nav>
      <div class="detail-body">
        <p v-if="detail.isPending.value" role="status">Loading skill…</p>
        <p v-if="detail.error.value" role="alert">{{ detail.error.value.message }}</p>
        <p v-if="error" role="alert" class="inline-error">{{ error }}</p>
        <template v-if="tab === 'overview' && detail.data.value"
          ><dl class="facts">
            <dt>Added by</dt>
            <dd>{{ author }}</dd>
            <dt>Source</dt>
            <dd>
              {{
                detail.data.value.origin === 'bundled'
                  ? 'K-Dense scientific skills'
                  : detail.data.value.source.kind
              }}<template v-if="detail.data.value.source.location">
                · {{ detail.data.value.source.location }}</template
              >
            </dd>
            <template v-if="detail.data.value.licence"
              ><dt>Licence</dt>
              <dd>{{ detail.data.value.licence }}</dd></template
            >
          </dl>
          <p v-if="formattingFailed" class="formatting-notice" role="status">
            Markdown formatting is unavailable; showing recorded text.
          </p>
          <div v-if="formattingFailed" class="recorded-text-fallback">{{ instructions }}</div>
          <RecordedMarkdown v-else :content="instructions"
        /></template>
        <template v-if="tab === 'files' && detail.data.value"
          ><nav class="file-tree" aria-label="Skill files">
            <template v-for="f in tree" :key="f.path + (f.directory ? '/' : '')"
              ><span
                v-if="f.directory"
                class="file-directory"
                :style="{ paddingLeft: f.depth * 14 + 5 + 'px' }"
                >{{ f.label }}</span
              ><button
                v-else
                :aria-label="f.path"
                :style="{ paddingLeft: f.depth * 14 + 5 + 'px' }"
                :aria-pressed="path === f.path"
                @click="path = f.path"
              >
                {{ f.label }}
              </button></template
            >
          </nav>
          <p v-if="file.isPending.value" role="status">Loading file…</p>
          <p v-else-if="file.error.value" role="alert">{{ file.error.value.message }}</p>
          <template v-else-if="metadata?.binary && !previewableBinary"
            ><strong>Binary file</strong>
            <p class="muted">{{ metadata.size }} bytes · SHA256 {{ metadata.sha256 }}</p></template
          >
          <SkillFileViewer v-else :path="path" :content="preview" :blob="file.data.value" />
        </template>
        <template v-if="tab === 'editing'"
          ><label class="editor-label" for="skill-content">SKILL.md</label
          ><textarea
            v-if="form.draft.value"
            id="skill-content"
            v-model="form.draft.value.content"
            class="skill-editor"
            spellcheck="false"
            :readonly="!saved"
          />
          <p class="muted">
            Supporting files are read-only. Saving creates a new version for future studies.
          </p>
          <p v-if="form.stale.value" role="status">
            The saved version has changed. Your draft is preserved.
          </p>
          <p v-if="!saved && !detail.isPending.value" role="status">
            This skill is no longer editable. Your draft is preserved.
          </p>
          <button v-if="form.stale.value || refused" class="button" @click="form.reset">
            Reload saved version
          </button></template
        >
      </div>
      <footer class="detail-footer">
        <p>
          {{
            detail.data.value?.origin === 'bundled'
              ? 'Bundled skills are read-only. Import your own skill under a different name to customize instructions.'
              : 'Only the importer and workspace admins can edit or delete this skill. Studies already using it keep their routed version.'
          }}
        </p>
        <div v-if="tab === 'editing'" class="edit-actions">
          <button class="button" @click="cancelEdit">Cancel</button
          ><button
            class="button primary"
            :disabled="!form.canSave.value || busy || refused"
            @click="save"
          >
            Save changes
          </button>
        </div>
      </footer>
      <Modal v-if="deleting" class="delete-modal" title="Delete skill" @close="deleting = false">
        <div class="delete-confirm">
          <h3>Delete {{ detail.data.value?.name }}?</h3>
          <p>Studies already using it keep their routed version.</p>
          <div class="delete-actions">
            <button class="button" :disabled="busy" @click="deleting = false">Keep skill</button>
            <button class="button danger" :disabled="!saved || busy" @click="remove(close)">
              Delete skill
            </button>
          </div>
        </div>
      </Modal>
    </section>
  </Modal>
</template>
<style scoped>
.recorded-text-fallback {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.formatting-notice {
  color: var(--muted);
  font-size: 13px;
}
.skill-modal {
  width: 640px;
}
.skill-detail {
  width: 640px;
  max-width: 100%;
  display: flex;
  flex-direction: column;
  max-height: calc(90vh - 2px);
  background: var(--surface);
}
.detail-head {
  padding: 20px;
  border-bottom: 1px solid var(--border);
}
.crumb {
  font-size: 13px;
  color: var(--muted);
  margin-bottom: 10px;
}
.detail-title {
  display: flex;
  gap: 12px;
  align-items: center;
}
.detail-title h2 {
  font-size: 17px;
  font-weight: 600;
  margin: 0;
  flex: 1;
  overflow-wrap: anywhere;
}
.icon-button {
  display: grid;
  place-items: center;
  background: none;
  border: 0;
  color: var(--muted);
  cursor: pointer;
  padding: 3px;
}
.lede {
  font-size: 13px;
  line-height: 1.5;
  color: var(--muted);
  margin: 9px 0 13px;
}
.chips {
  display: flex;
  gap: 7px;
  flex-wrap: wrap;
}
.chips span {
  background: var(--input);
  border-radius: 5px;
  padding: 4px 7px;
  font-size: 11px;
  color: var(--muted);
}
.chips .script-chip {
  color: #666a6d;
  border: 1px solid var(--border);
}
.detail-tabs {
  padding: 0 20px;
  display: flex;
  gap: 20px;
  border-bottom: 1px solid var(--border);
}
.detail-tabs button {
  border: 0;
  background: none;
  padding: 12px 0;
  color: var(--muted);
  cursor: pointer;
}
.detail-tabs button[aria-selected='true'] {
  border-bottom: 2px solid var(--text);
  color: var(--text);
}
.detail-body {
  padding: 20px;
  overflow: auto;
  scrollbar-width: none;
  flex: 1;
  min-height: 230px;
}
.detail-body::-webkit-scrollbar {
  display: none;
}
.facts {
  display: grid;
  grid-template-columns: 84px 1fr;
  gap: 11px;
  font-size: 13px;
  margin: 0 0 24px;
}
.facts dt {
  color: var(--muted);
}
.facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.file-tree {
  background: var(--canvas);
  border-radius: 7px;
  padding: 12px;
  margin-bottom: 18px;
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.file-directory {
  font:
    12px/2 ui-monospace,
    monospace;
  color: var(--muted);
}
.file-tree button {
  font:
    12px/2 ui-monospace,
    monospace;
  color: var(--text);
  text-align: left;
  border: 0;
  background: transparent;
  border-radius: 4px;
  cursor: pointer;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  padding: 0 5px;
}
.file-tree button[aria-pressed='true'] {
  background: var(--selected);
}
.file-preview {
  font:
    12px/1.7 ui-monospace,
    monospace;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.editor-label {
  display: block;
  font-size: 13px;
  margin-bottom: 10px;
}
.skill-editor {
  width: 100%;
  min-height: 300px;
  background: var(--input);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 7px;
  padding: 12px;
  font:
    12px/1.7 ui-monospace,
    monospace;
  resize: none;
}
.detail-footer {
  border-top: 1px solid var(--border);
  padding: 14px 20px;
}
.detail-footer p,
.muted {
  font-size: 13px;
  line-height: 1.5;
  color: var(--muted);
  margin: 0;
  overflow-wrap: anywhere;
}
.edit-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 12px;
}
.delete-modal {
  width: 420px;
  background: var(--surface);
}
.delete-confirm {
  padding: 24px;
}
.delete-confirm h3 {
  font-size: 16px;
  line-height: 1.4;
  margin: 0 0 12px;
  overflow-wrap: anywhere;
}
.delete-confirm p {
  color: var(--muted);
  font-size: 13px;
  line-height: 1.5;
}
.delete-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 24px;
}
.delete-actions .danger {
  background: var(--danger-bg);
  color: var(--danger-text);
}
button:focus-visible,
textarea:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: 2px;
}
</style>
