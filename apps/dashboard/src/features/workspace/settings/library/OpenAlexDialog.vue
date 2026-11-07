<script lang="ts">
// OpenAlex's published daily budgets: a free key holds ten times a keyless one.
export const budget = (keySet?: boolean) =>
  keySet ? 'Key saved · about 1,000 searches a day' : 'No key · about 100 searches a day';
</script>
<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue';
import Modal from '../../../../ui/Modal.vue';
import Icon from '../../../../ui/Icon.vue';
import { useLibraryMutations, useOpenAlexConnector } from '../../../../api/library';
import { useWorkspaces } from '../../../../api/queries';
import { workspaceId } from '../../../../api/session';
const emit = defineEmits<{ close: [] }>();
const openalex = useOpenAlexConnector(),
  workspaces = useWorkspaces(),
  mutations = useLibraryMutations();
const allowed = computed(
  () => workspaces.data.value?.find((w) => w.id === workspaceId.value)?.role === 'owner',
);
const saved = computed(() => Boolean(openalex.data.value?.key_set));
const key = ref(''),
  busy = ref(false),
  error = ref('');
let visit = 0;
onUnmounted(() => visit++);
async function save(value: string) {
  const entry = openalex.data.value;
  if (!entry || !allowed.value || busy.value) return;
  const token = visit,
    ws = workspaceId.value;
  busy.value = true;
  error.value = '';
  try {
    await mutations.setConnector('openalex', { revision: entry.revision, key: value });
    if (token === visit && ws === workspaceId.value) key.value = '';
  } catch (e) {
    if (token === visit && ws === workspaceId.value) {
      error.value = (e as Error).message;
      await openalex.refetch();
    }
  } finally {
    if (token === visit && ws === workspaceId.value) busy.value = false;
  }
}
</script>
<template>
  <Modal v-slot="{ close }" class="openalex-modal" title="OpenAlex" @close="emit('close')"
    ><section class="openalex">
      <header>
        <div>
          <h2>OpenAlex</h2>
          <p>
            Papers, preprints and abstracts across all fields, searched through the sources server.
          </p>
          <div class="chips"><span>Literature</span><span>key</span></div>
        </div>
        <button class="icon-button" aria-label="Close OpenAlex" @click="close">
          <Icon name="close" :size="18" />
        </button>
      </header>
      <div class="body">
        <h3>Overview</h3>
        <dl v-if="openalex.data.value" class="facts">
          <dt>Availability</dt>
          <dd>
            {{
              openalex.data.value.enabled
                ? 'Available to this workspace'
                : openalex.data.value.reason
            }}
          </dd>
          <dt>Origin</dt>
          <dd>Built into the sources server</dd>
          <dt>Scope</dt>
          <dd>Workspace</dd>
        </dl>
        <p v-else-if="openalex.isError.value" class="inline-error" role="alert">
          Could not load OpenAlex availability.
        </p>
        <p v-else role="status" class="hint">Loading OpenAlex availability…</p>
        <div v-if="saved" class="notice saved" role="status">
          <strong>Key saved: about 1,000 OpenAlex searches a day for this workspace.</strong>
          <p>
            Studies in this workspace search under its own key. The budget resets at midnight UTC.
          </p>
        </div>
        <div v-else class="notice">
          <strong>A free API key gives this workspace about 10× more OpenAlex searches.</strong>
          <ul>
            <li class="now"><span>Without a key</span><b>~100</b> searches a day</li>
            <li><span>With a free key</span><b>~1,000</b> searches a day</li>
          </ul>
          <p>
            Without a key, studies share the server's keyless budget. Once it runs out, OpenAlex
            searches fail until it resets at midnight UTC, and agents work without its literature. A
            key is free at
            <a href="https://openalex.org" target="_blank" rel="noopener">openalex.org</a>.
          </p>
        </div>
        <label class="field"
          >API key<input
            :value="key"
            type="password"
            aria-label="OpenAlex API key"
            autocomplete="new-password"
            :placeholder="saved ? 'Enter a new key to replace the saved one' : 'Enter an API key'"
            :disabled="!allowed || busy"
            @input="key = ($event.target as HTMLInputElement).value"
          />
        </label>
        <p class="hint">Keys are sealed on the server and cannot be read back.</p>
        <p v-if="!allowed" class="hint">Only the workspace owner can save a key.</p>
        <button v-if="saved && allowed" class="link" :disabled="busy" @click="save('')">
          Clear saved key
        </button>
        <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
      </div>
      <footer>
        <button class="button" @click="close">Cancel</button
        ><button
          class="button primary"
          :disabled="!allowed || busy || !key.trim()"
          @click="save(key.trim())"
        >
          Save
        </button>
      </footer>
    </section></Modal
  >
</template>
<style scoped>
.openalex-modal,
.openalex {
  width: 620px;
  max-width: 100%;
}
.openalex {
  display: flex;
  flex-direction: column;
  max-height: calc(90vh - 2px);
  background: var(--surface);
}
header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  padding: 21px 28px;
}
h2 {
  font-size: 18px;
  font-weight: 600;
  margin: 0;
}
header p {
  font-size: 13px;
  color: var(--muted);
  margin: 4px 0 0;
}
.chips {
  display: flex;
  gap: 6px;
  margin-top: 12px;
}
.chips span {
  font-size: 11px;
  background: var(--hover);
  color: var(--muted);
  border-radius: 5px;
  padding: 4px 7px;
}
.icon-button {
  border: 0;
  background: none;
  color: var(--muted);
  cursor: pointer;
}
.body {
  padding: 16px 24px 24px;
  overflow: auto;
  min-height: 0;
  scrollbar-width: none;
}
.body::-webkit-scrollbar {
  display: none;
}
.body h3 {
  font-size: 16px;
  margin: 0 0 16px;
}
.facts {
  display: grid;
  grid-template-columns: 100px 1fr;
  gap: 14px;
  font-size: 13px;
  line-height: 1.5;
  margin: 0 0 24px;
}
.facts dt {
  color: var(--muted);
}
.facts dd {
  margin: 0;
}
.notice {
  padding: 12px 14px;
  border-radius: 9px;
  border: 1px solid var(--recommend-border);
  background: var(--recommend-bg);
  font-size: 13px;
  line-height: 1.5;
}
.notice strong {
  color: var(--recommend-text);
  font-weight: 600;
}
.notice.saved {
  border-color: var(--border);
  background: var(--fill-inset);
}
.notice.saved strong {
  color: var(--text);
}
.notice ul {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  margin: 10px 0 0;
  padding: 0;
  list-style: none;
}
.notice li {
  background: var(--surface);
  border: 1px solid var(--recommend-border);
  border-radius: 7px;
  padding: 8px 10px;
}
.notice li.now {
  border-color: var(--recommend);
}
.notice li span {
  display: block;
  font-size: 13px;
  color: var(--muted);
}
.notice li b {
  font-size: 15px;
  font-weight: 600;
}
.notice p {
  margin: 10px 0 0;
  font-size: 13px;
  color: var(--muted);
}
.notice a {
  color: var(--primary-text);
}
.field {
  display: block;
  font-size: 13px;
  margin: 20px 0 0;
}
.field input {
  display: block;
  width: 100%;
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 7px;
  padding: 8px 10px;
  margin-top: 7px;
  color: var(--text);
}
.hint {
  font-size: 13px;
  color: var(--muted);
  line-height: 1.5;
  margin: 6px 0 0;
}
.link {
  border: 0;
  background: none;
  padding: 0;
  margin-top: 8px;
  color: var(--primary-text);
  font-size: 13px;
  cursor: pointer;
}
footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  border-top: 1px solid var(--border);
  padding: 14px 28px;
}
.inline-error {
  overflow-wrap: anywhere;
}
button:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: 2px;
}
@media (max-width: 560px) {
  .notice ul {
    grid-template-columns: 1fr;
  }
}
</style>
