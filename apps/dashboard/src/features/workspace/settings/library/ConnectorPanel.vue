<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { workspaceId } from '../../../../api/session';
import { useConnectorLibrary, useWorkspaceMCPServers } from '../../../../api/library';
import { useWorkspaces } from '../../../../api/queries';
import { matchesQuery } from '../../../../lib/search';
import Icon from '../../../../ui/Icon.vue';
import CapabilityList from './CapabilityList.vue';
import ConnectorDialog from './ConnectorDialog.vue';
import AddConnectorDialog from './AddConnectorDialog.vue';
import OpenAlexDialog from './OpenAlexDialog.vue';
const catalogue = useConnectorLibrary(),
  custom = useWorkspaceMCPServers(),
  workspaces = useWorkspaces();
const query = ref(''),
  field = ref(''),
  selected = ref(''),
  kind = ref<'catalogue' | 'custom'>('catalogue'),
  showDetail = ref(false),
  showOpenAlex = ref(false),
  adding = ref(false),
  editing = ref(false);
const allowed = computed(() =>
  ['owner', 'admin'].includes(
    workspaces.data.value?.find((w) => w.id === workspaceId.value)?.role ?? '',
  ),
);
const fields = computed(() =>
  [
    ...new Set([
      'Literature',
      ...(catalogue.data.value ?? []).map((c) => c.domain).filter(Boolean),
      ...((custom.data.value ?? []).length ? ['Workspace'] : []),
    ]),
  ].sort(),
);
const scientific = computed(() =>
  (catalogue.data.value ?? []).filter(
    (c) =>
      matchesQuery(`${c.name} ${c.domain} ${c.purpose}`, query.value) &&
      (!field.value || c.domain === field.value),
  ),
);
// OpenAlex is built into the sources server and listed with Literature.
const literature = computed(
  () =>
    (!field.value || field.value === 'Literature') &&
    matchesQuery('OpenAlex Literature papers preprints abstracts', query.value),
);
const own = computed(() =>
  (custom.data.value ?? []).filter(
    (c) =>
      (!field.value || field.value === 'Workspace') &&
      matchesQuery(`${c.name} ${c.transport}`, query.value),
  ),
);
const rows = computed(() =>
  [
    ...(literature.value ? [{ type: 'openalex' as const, name: 'OpenAlex' }] : []),
    ...scientific.value.map((connector) => ({
      type: 'catalogue' as const,
      name: connector.name,
      connector,
    })),
    ...own.value.map((connector) => ({ type: 'custom' as const, name: connector.name, connector })),
  ].sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' })),
);
function open(id: string, type: 'catalogue' | 'custom') {
  selected.value = id;
  kind.value = type;
  showDetail.value = true;
}
function created(id: string) {
  adding.value = false;
  open(id, 'custom');
}
watch(
  workspaceId,
  () => {
    selected.value = '';
    showDetail.value = false;
    adding.value = false;
    editing.value = false;
    showOpenAlex.value = false;
    query.value = '';
    field.value = '';
  },
  { flush: 'sync' },
);
</script>
<template>
  <section>
    <div class="heading">
      <h2>Connectors</h2>
      <button v-if="allowed" class="button primary" @click="adding = true">
        <Icon name="plus" :size="14" />Add connector
      </button>
    </div>
    <CapabilityList
      v-model:query="query"
      v-model:field="field"
      :fields="fields"
      noun="connectors"
      :loading="catalogue.isPending.value || custom.isPending.value"
      :error="catalogue.error.value?.message || custom.error.value?.message"
      :count="rows.length"
      @retry="
        catalogue.refetch();
        custom.refetch();
      "
    >
      <template
        v-for="row in rows"
        :key="row.type + ':' + (row.type === 'custom' ? row.connector.id : row.name)"
      >
        <div v-if="row.type === 'openalex'" class="connector-entry">
          <button
            class="connector-row"
            :class="{ chosen: showOpenAlex }"
            aria-label="OpenAlex"
            aria-description="Built-in scientific connector"
            @click="showOpenAlex = true"
          >
            <div>
              <strong>OpenAlex</strong><small>Built-in scientific connector</small>
              <p>Papers, preprints and abstracts across all fields, arXiv included. Built in.</p>
            </div>
          </button>
        </div>
        <div v-else-if="row.type === 'catalogue'" class="connector-entry">
          <button
            class="connector-row"
            :class="{
              chosen: showDetail && selected === row.connector.name && kind === 'catalogue',
            }"
            :aria-label="row.connector.name"
            @click="open(row.connector.name, 'catalogue')"
          >
            <div>
              <strong>{{ row.connector.name }}</strong>
              <p>{{ row.connector.purpose }}</p>
            </div>
          </button>
        </div>
        <div v-else class="connector-entry">
          <button
            class="connector-row"
            :class="{ chosen: showDetail && selected === row.connector.id && kind === 'custom' }"
            :aria-label="row.connector.name"
            @click="open(row.connector.id, 'custom')"
          >
            <div>
              <strong>{{ row.connector.name }}</strong
              ><small
                >Custom MCP · {{ row.connector.transport === 'url' ? 'Remote URL' : 'Command' }}</small
              >
            </div>
          </button>
        </div>
      </template></CapabilityList
    ><ConnectorDialog
      v-if="showDetail && selected"
      :key="workspaceId + kind + selected"
      :connector-id="selected"
      :kind="kind"
      :can-edit="allowed"
      @edit="
        showDetail = false;
        editing = true;
      "
      @close="showDetail = false"
    /><OpenAlexDialog
      v-if="showOpenAlex"
      :key="workspaceId + ':openalex'"
      @close="showOpenAlex = false"
    /><AddConnectorDialog
      v-if="adding"
      :key="workspaceId"
      @close="adding = false"
      @created="created"
    /><AddConnectorDialog
      v-if="editing && selected"
      :key="workspaceId + ':edit:' + selected"
      :editing="custom.data.value?.find((c) => c.id === selected)"
      @close="editing = false"
      @created="
        editing = false;
        open($event, 'custom');
      "
    />
  </section>
</template>
<style scoped>
.heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin: 0 0 14px;
  min-height: 32px;
}
.heading h2 {
  font-size: 20px;
  font-weight: 600;
  line-height: 28px;
  margin: 0;
}
.heading .button {
  display: flex;
  gap: 6px;
  align-items: center;
}
h3 {
  font-size: 14px;
  font-weight: 500;
  margin: 24px 0 6px;
}
.section-note {
  font-size: 13px;
  color: var(--muted);
  line-height: 1.5;
  margin: 0 0 14px;
}
.budget {
  flex: none;
  font-size: 13px;
  white-space: nowrap;
  padding: 2px 8px;
  border-radius: 99px;
  border: 1px solid var(--recommend-border);
  background: var(--recommend-bg);
  color: var(--recommend-text);
}
.budget.saved {
  border-color: var(--border);
  background: var(--fill-chip);
  color: var(--text);
}
.connector-entry {
  display: flex;
  align-items: center;
  border-bottom: 1px solid var(--border);
}
.connector-row {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  text-align: left;
  border: 0;
  padding: 14px 7px;
  color: var(--text);
  background: none;
  cursor: pointer;
}
.connector-entry:hover,
.connector-entry:has(.connector-row.chosen) {
  background: var(--hover);
  border-radius: 7px;
}
.connector-row > div {
  flex: 1;
  min-width: 0;
}
.connector-row strong {
  font-size: 13px;
  font-weight: 500;
}
.connector-row p,
.connector-row small {
  display: block;
  font-size: 13px;
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  margin: 5px 0 0;
}
.connector-row small {
  display: inline;
  margin: 0 0 0 10px;
  font-size: 11px;
}
button:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: 2px;
}
</style>
