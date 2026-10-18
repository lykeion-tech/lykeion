<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue';
import Modal from '../../Modal.vue';
import Icon from '../../Icon.vue';
import RuntimeSkillImport from './RuntimeSkillImport.vue';
import { useLibraryMutations } from '../../../api/library';
import { useResourceDraft, guardResourceDrafts } from '../../../state/resource-draft';
import { workspaceId } from '../../../api/session';
const emit = defineEmits<{ close: []; created: [id: string] }>();
const method = ref<'menu' | 'manual' | 'local' | 'url' | 'runtime'>('menu');
const titles = {
  menu: 'Add skill',
  manual: 'Create manually',
  local: 'Import from local',
  url: 'Import from URL',
  runtime: 'Copy from runtime',
};
const descriptions = {
  menu: 'Choose how to add a skill.',
  manual: 'Start from a blank SKILL.md.',
  local: 'Pick a folder with SKILL.md, or a .skill / .zip archive.',
  url: 'Fetch a published skill by URL.',
  runtime: 'Copy selected skills into the workspace library.',
};
const empty = { revision: 0, value: { name: '', description: '', url: '', files: [] as string[] } };
const form = useResourceDraft(
  computed(() => workspaceId.value + ':add-skill'),
  computed(() => empty),
);
function field(key: 'name' | 'description' | 'url') {
  return computed({
    get: () => form.draft.value?.[key] ?? '',
    set: (value: string) => {
      if (form.draft.value) form.draft.value[key] = value;
    },
  });
}
const name = field('name'),
  description = field('description'),
  url = field('url');
const error = ref(''),
  busy = ref(false),
  files = ref<File[]>([]),
  kind = ref<'archive' | 'folder'>('archive');
const archiveInput = ref<HTMLInputElement>(),
  folderInput = ref<HTMLInputElement>();
const mutations = useLibraryMutations();
let visit = 0;
onUnmounted(() => visit++);
const valid = computed(() =>
  method.value === 'manual'
    ? !!name.value.trim()
    : method.value === 'url'
      ? !!url.value.trim()
      : method.value === 'local'
        ? !!files.value.length
        : false,
);
function choose(event: Event, mode: 'archive' | 'folder') {
  kind.value = mode;
  files.value = Array.from((event.target as HTMLInputElement).files ?? []);
  if (form.draft.value)
    form.draft.value.files = files.value.map(
      (f) => `${f.webkitRelativePath || f.name}:${f.size}:${f.lastModified}`,
    );
  error.value = '';
}
async function submit() {
  if (!valid.value || busy.value) return;
  const own = visit,
    ws = workspaceId.value;
  busy.value = true;
  error.value = '';
  try {
    const response =
      method.value === 'manual'
        ? await mutations.createSkill({ name: name.value.trim(), description: description.value })
        : method.value === 'url'
          ? await mutations.importSkillURL(url.value.trim())
          : kind.value === 'archive'
            ? await mutations.importSkillArchive(files.value[0]!, files.value[0]!.name)
            : await mutations.importSkillFolder(
                files.value.map((f) => ({ path: f.webkitRelativePath || f.name, data: f })),
              );
    if (own === visit && ws === workspaceId.value) emit('created', response.id);
  } catch (e) {
    if (own === visit && ws === workspaceId.value) error.value = (e as Error).message;
  } finally {
    if (own === visit && ws === workspaceId.value) busy.value = false;
  }
}
</script>
<template>
  <Modal
    class="add-skill-modal"
    :class="{ runtime: method === 'runtime' }"
    :before-close="guardResourceDrafts"
    :title="titles[method]"
    v-slot="{ close }"
    @close="emit('close')"
    ><section class="add-skill" :class="{ runtime: method === 'runtime' }">
      <header class="add-head">
        <button
          v-if="method !== 'menu'"
          class="icon-button"
          aria-label="Back to methods"
          :disabled="busy"
          @click="
            method = 'menu';
            error = '';
          "
        >
          <Icon name="back" :size="18" />
        </button>
        <div>
          <h2>{{ titles[method] }}</h2>
          <p>{{ descriptions[method] }}</p>
        </div>
        <button class="icon-button close" aria-label="Close add skill" @click="close">
          <Icon name="close" :size="18" />
        </button>
      </header>
      <div class="add-body">
        <template v-if="method === 'menu'"
          ><button
            v-for="id in ['manual', 'local', 'url', 'runtime'] as const"
            :key="id"
            class="import-option"
            @click="method = id"
          >
            <strong>{{ titles[id] }}</strong
            ><span>{{ descriptions[id] }}</span>
          </button></template
        ><template v-else-if="method === 'manual'"
          ><label class="field"
            >Name<input
              v-model="name"
              aria-label="Name"
              placeholder="e.g. review-helper"
              :disabled="busy"
              autofocus
            /><small>Must be unique within the workspace.</small></label
          ><label class="field"
            >Description<textarea
              v-model="description"
              aria-label="Description"
              placeholder="One sentence on when an agent should use this skill."
              :disabled="busy"
            /></label></template
        ><template v-else-if="method === 'local'"
          ><div class="local-actions">
            <button class="button" :disabled="busy" @click="folderInput?.click()">
              <Icon name="folder" :size="14" />Choose folder</button
            ><button class="button" :disabled="busy" @click="archiveInput?.click()">
              <Icon name="upload" :size="14" />Choose .skill / .zip
            </button>
          </div>
          <input
            ref="archiveInput"
            hidden
            data-archive
            type="file"
            accept=".skill,.zip"
            @change="choose($event, 'archive')"
          /><input
            ref="folderInput"
            hidden
            data-folder
            type="file"
            webkitdirectory
            multiple
            @change="choose($event, 'folder')"
          />
          <div v-if="files.length" class="local-preview">
            <strong>{{
              kind === 'archive' ? files[0]?.name : files[0]?.webkitRelativePath.split('/')[0]
            }}</strong>
            <p>
              {{ files.length }} {{ kind === 'archive' ? 'archive selected' : 'files selected' }}
            </p>
            <p
              v-if="
                kind === 'folder' &&
                files.some((f) => (f.webkitRelativePath || f.name).split('/').includes('scripts'))
              "
            >
              Contains scripts
            </p>
          </div></template
        ><template v-else-if="method === 'url'"
          ><label class="field"
            >Skill URL<input
              v-model="url"
              aria-label="Skill URL"
              placeholder="https://…"
              :disabled="busy"
          /></label>
          <p class="hint">Import a published skill from GitHub, Skills.sh or ClawHub.</p></template
        ><RuntimeSkillImport
          v-else-if="method === 'runtime'"
          @created="emit('created', $event)"
          @close="close"
        />
        <p class="workspace-note">
          Skills you add are shared with the whole workspace. Their scripts may run on colleagues’
          machines when used by agents.
        </p>
        <p v-if="error" role="alert" class="inline-error import-error">{{ error }}</p>
        <p v-if="busy" role="status">
          {{ method === 'manual' ? 'Creating skill…' : 'Importing skill…' }}
        </p>
      </div>
      <footer v-if="method !== 'menu' && method !== 'runtime'" class="add-foot">
        <button class="button" @click="close">Cancel</button
        ><button class="button primary" :disabled="!valid || busy" @click="submit">
          {{ method === 'manual' ? 'Create skill' : 'Import' }}
        </button>
      </footer>
    </section></Modal
  >
</template>
<style scoped>
.add-skill-modal {
  width: 450px;
}
.add-skill-modal.runtime {
  width: 660px;
}
.add-skill {
  width: 450px;
  max-width: 100%;
  display: flex;
  flex-direction: column;
  max-height: calc(90vh - 2px);
}
.add-skill.runtime {
  width: 660px;
}
.add-head {
  padding: 20px;
  border-bottom: 1px solid var(--border);
  display: flex;
  gap: 12px;
  align-items: flex-start;
}
.add-head h2 {
  font-size: 16px;
  font-weight: 500;
  margin: 0;
}
.add-head p {
  font-size: 12px;
  color: var(--muted);
  margin: 4px 0 0;
}
.icon-button {
  border: 0;
  background: none;
  color: var(--muted);
  padding: 0;
  cursor: pointer;
}
.close {
  margin-left: auto;
}
.add-body {
  padding: 20px;
  overflow: auto;
}
.import-option {
  display: block;
  width: 100%;
  border: 1px solid var(--border);
  background: transparent;
  color: var(--text);
  border-radius: 8px;
  text-align: left;
  padding: 13px;
  margin-bottom: 9px;
  cursor: pointer;
}
.import-option:hover {
  background: var(--hover);
}
.import-option strong {
  font-weight: 500;
}
.import-option span {
  display: block;
  font-size: 12px;
  color: var(--muted);
  margin-top: 4px;
}
.field {
  display: block;
  margin-bottom: 18px;
  font-size: 12px;
}
.field:last-child {
  margin-bottom: 0;
}
.field input,
.field textarea {
  display: block;
  width: 100%;
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 7px;
  padding: 8px 10px;
  color: var(--text);
  font: inherit;
  margin-top: 7px;
  min-height: 34px;
}
.field textarea {
  height: 74px;
  resize: vertical;
}
.field small,
.hint {
  font-size: 12px;
  color: var(--muted);
  margin: 6px 0 0;
  line-height: 1.5;
}
.field small {
  display: block;
}
.workspace-note {
  font-size: 11px;
  color: var(--muted);
  margin: 18px 0 0;
  line-height: 1.5;
}
.add-foot {
  display: flex;
  gap: 8px;
  align-items: center;
  justify-content: flex-end;
  padding: 13px 20px;
  border-top: 1px solid var(--border);
}
.local-actions {
  display: flex;
  gap: 10px;
}
.local-actions .button {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
}
.local-preview {
  border: 1px solid var(--border);
  border-radius: 7px;
  padding: 12px;
  margin-top: 14px;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.local-preview strong {
  font-weight: 500;
}
.local-preview p {
  color: var(--muted);
  margin: 6px 0 0;
}
.import-error {
  max-height: 150px;
  overflow: auto;
  overflow-wrap: anywhere;
}
button:focus-visible,
input:focus-visible,
textarea:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 2px;
}
</style>
