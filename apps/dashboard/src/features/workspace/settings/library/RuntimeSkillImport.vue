<script setup lang="ts">
import SelectPicker from '../../../../ui/SelectPicker.vue';
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { useRuntimes, useMe } from '../../../../api/queries';
import { useResourceDraft } from '../../../../state/resource-draft';
import { workspaceId } from '../../../../api/session';
import {
  useLibraryMutations,
  useRuntimeSkillRequest,
  type RuntimeSkillInventory,
  type RuntimeSkillOperation,
} from '../../../../api/library';
const emit = defineEmits<{ created: [id: string]; close: [] }>();
const runtimes = useRuntimes(),
  me = useMe(),
  mutations = useLibraryMutations();
const runtime = ref(''),
  request = ref(''),
  operation = ref<RuntimeSkillOperation>('list'),
  query = ref(''),
  inventory = ref<RuntimeSkillInventory[]>([]),
  error = ref(''),
  starting = ref(false),
  changing = ref(false);
const empty = { revision: 0, value: { selected: [] as string[] } };
const selection = useResourceDraft(
  computed(() => `${workspaceId.value}:runtime-import:${runtime.value}`),
  computed(() => {
    void runtime.value;
    return { ...empty, value: { selected: [] as string[] } };
  }),
);
const selected = computed({
  get: () => selection.draft.value?.selected ?? [],
  set: (value: string[]) => {
    if (selection.draft.value) selection.draft.value.selected = value;
  },
});
const job = useRuntimeSkillRequest(runtime, request, operation);
const owned = computed(() =>
  (runtimes.data.value ?? []).filter((r) => r.owner_id === me.data.value?.id),
);
const rows = computed(() =>
  inventory.value.filter((s) =>
    `${s.name} ${s.description}`.toLowerCase().includes(query.value.toLowerCase()),
  ),
);
const active = computed(
  () => starting.value || ['pending', 'running'].includes(job.data.value?.status ?? ''),
);
const allSelected = computed(
  () => rows.value.length > 0 && rows.value.every((s) => selected.value.includes(s.key)),
);
const label = (h: string) => ({ claude: 'Claude Code', codex: 'Codex' })[h] ?? h;
let visit = 0;
let timer: ReturnType<typeof setInterval>;
onMounted(() => {
  timer = setInterval(() => {
    if (
      request.value &&
      active.value &&
      !starting.value &&
      !job.isFetching.value &&
      !job.isError.value
    )
      void job.refetch();
  }, 500);
});
onUnmounted(() => {
  visit++;
  clearInterval(timer);
});
watch(
  [workspaceId, () => me.data.value?.id],
  () => {
    visit++;
    runtime.value = '';
    request.value = '';
    inventory.value = [];
    selected.value = [];
  },
  { flush: 'sync' },
);
watch(
  () => job.data.value,
  (current) => {
    if (
      current?.operation === 'import' &&
      ['completed', 'failed', 'timeout'].includes(current.status)
    )
      selected.value = [];
    if (current?.operation === 'list' && current.status === 'completed')
      inventory.value = current.result.skills ?? [];
  },
);
async function changeRuntime(event: Event) {
  const input = event.target as HTMLSelectElement,
    next = input.value;
  input.value = runtime.value;
  if (next === runtime.value || active.value || changing.value) return;
  const own = visit,
    ws = workspaceId.value,
    previous = runtime.value;
  changing.value = true;
  try {
    if (!(await selection.reset())) return;
    if (own !== visit || ws !== workspaceId.value || runtime.value !== previous) return;
    runtime.value = next;
    await discover();
  } finally {
    changing.value = false;
  }
}
async function discover() {
  const own = ++visit,
    ws = workspaceId.value,
    id = runtime.value;
  request.value = '';
  operation.value = 'list';
  inventory.value = [];
  selected.value = [];
  query.value = '';
  error.value = '';
  if (!id) return;
  starting.value = true;
  try {
    const response = await mutations.listRuntimeSkills(id);
    if (own === visit && ws === workspaceId.value && id === runtime.value)
      request.value = response.id;
  } catch (e) {
    if (own === visit) error.value = (e as Error).message;
  } finally {
    if (own === visit) starting.value = false;
  }
}
async function copy() {
  if (active.value || !selected.value.length) return;
  const own = ++visit,
    ws = workspaceId.value,
    id = runtime.value;
  starting.value = true;
  error.value = '';
  request.value = '';
  operation.value = 'import';
  try {
    const response = await mutations.importRuntimeSkills(id, [...selected.value]);
    if (own === visit && ws === workspaceId.value && id === runtime.value)
      request.value = response.id;
  } catch (e) {
    if (own === visit) error.value = (e as Error).message;
  } finally {
    if (own === visit) starting.value = false;
  }
}
function done() {
  const current = job.data.value;
  const items = current?.result.items ?? [];
  if (current?.keys.length === 1 && items.length === 1 && items[0]?.status === 'imported')
    emit('created', items[0].skill_id);
  else emit('close');
}
function toggleAll() {
  selected.value = allSelected.value
    ? selected.value.filter((k) => !rows.value.some((s) => s.key === k))
    : [...new Set([...selected.value, ...rows.value.map((s) => s.key)])];
}
</script>
<template>
  <div class="runtime-import">
    <label class="field"
      >Runtime<SelectPicker
        ><select
          :value="runtime"
          aria-label="Runtime"
          :disabled="active || changing"
          @change="changeRuntime"
        >
          <option value="">Choose runtime</option>
          <option v-for="r in owned" :key="r.id" :value="r.id" :disabled="r.status !== 'online'">
            {{ r.custom_name || r.device_name }} · {{ label(r.harness)
            }}{{ r.status === 'offline' ? ' · Offline' : '' }}
          </option>
        </select></SelectPicker
      ></label
    >
    <p v-if="runtimes.isPending.value" role="status">Loading runtimes…</p>
    <p v-else-if="runtimes.error.value" role="alert">{{ runtimes.error.value.message }}</p>
    <p v-else-if="!owned.length" class="empty">
      Connect a runtime you own to copy its installed skills.
    </p>
    <input
      v-model="query"
      type="search"
      aria-label="Search local skills"
      placeholder="Search local skills"
      :disabled="!inventory.length || active"
    />
    <p v-if="error || job.error.value" role="alert">
      {{ error || job.error.value?.message }}
      <button
        v-if="request && job.error.value"
        class="button"
        :disabled="job.isFetching.value"
        @click="job.refetch()"
      >
        Retry
      </button>
    </p>
    <p v-if="active" role="status">
      {{ operation === 'list' ? 'Discovering installed skills…' : 'Importing selected skills…' }}
    </p>
    <p
      v-if="job.data.value?.status === 'failed' || job.data.value?.status === 'timeout'"
      role="alert"
    >
      {{ job.data.value.error || `Runtime request ${job.data.value.status}.` }}
    </p>
    <template
      v-if="operation === 'list' && !active && request && job.data.value?.status === 'completed'"
      ><label v-if="rows.length" class="runtime-row"
        ><input type="checkbox" :checked="allSelected" @change="toggleAll" /><span
          >Select all</span
        ></label
      ><label v-for="s in rows" :key="s.key" class="runtime-row"
        ><input v-model="selected" type="checkbox" :value="s.key" /><span
          ><strong>{{ s.name }}</strong
          ><small>{{ s.description }}</small
          ><small>{{ s.source_path }} · {{ s.file_count }} files</small></span
        ></label
      >
      <p v-if="!rows.length" class="empty">
        {{
          query
            ? 'No local skills match your search.'
            : 'No installed skills found in this runtime.'
        }}
      </p></template
    >
    <p v-else-if="!runtime" class="empty">
      Choose a connected runtime to browse its installed skills.
    </p>
    <ul v-if="operation === 'import' && job.data.value?.result.items?.length" class="outcomes">
      <li v-for="item in job.data.value.result.items" :key="item.key">
        <span
          >{{ inventory.find((s) => s.key === item.key)?.name || item.key }} ·
          {{ item.status === 'imported' ? 'Imported' : item.status
          }}<template v-if="item.status !== 'imported'"> · {{ item.error }}</template></span
        >
      </li>
    </ul>
    <div class="runtime-actions">
      <span
        >{{
          operation === 'import'
            ? (job.data.value?.keys.length ?? selected.length)
            : selected.length
        }}
        selected</span
      ><button
        v-if="operation === 'list'"
        class="button primary"
        :disabled="active || !selected.length || job.isError.value"
        @click="copy"
      >
        Import to workspace</button
      ><template v-else-if="!active"
        ><button class="button" @click="discover">Browse again</button
        ><button v-if="job.data.value" class="button primary" @click="done">Done</button></template
      >
    </div>
  </div>
</template>
<style scoped>
.field {
  display: block;
  font-size: 12px;
  margin-bottom: 18px;
}
.field select {
  margin-top: 7px;
}
select,
input[type='search'] {
  width: 100%;
  background: var(--input);
  border: 1px solid var(--border);
  color: var(--text);
  border-radius: 7px;
  padding: 8px 10px;
  font: inherit;
}
.runtime-row {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  padding: 12px 0;
  border-bottom: 1px solid var(--border);
  font-size: 12px;
}
.runtime-row input {
  margin-top: 2px;
}
.runtime-row strong {
  font-weight: 500;
}
.runtime-row small {
  display: block;
  margin-top: 4px;
  color: var(--muted);
  overflow-wrap: anywhere;
}
.empty {
  min-height: 120px;
  display: grid;
  place-items: center;
  color: var(--muted);
  text-align: center;
  font-size: 12px;
}
.runtime-actions {
  display: flex;
  gap: 12px;
  justify-content: space-between;
  align-items: center;
  padding-top: 20px;
  font-size: 12px;
  color: var(--muted);
}
.outcomes {
  list-style: none;
  padding: 0;
}
.outcomes li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 0;
  border-bottom: 1px solid var(--border);
  font-size: 12px;
  overflow-wrap: anywhere;
}
input:focus-visible,
select:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: 2px;
}
</style>
