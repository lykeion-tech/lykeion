<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { workspaceId } from '../../../api/session';
import {
  useConnectorLibrary,
  useOpenAlexConnector,
  useWorkspaceMCPServers,
} from '../../../api/library';
import { useWorkspaces } from '../../../api/queries';
import { matchesQuery } from '../../../lib/search';
import Icon from '../../Icon.vue';
import AvailabilitySwitch from './AvailabilitySwitch.vue';
import { useAvailabilityChanges } from '../../../state/availability';
const policy = useAvailabilityChanges();
import CapabilityList from './CapabilityList.vue';
import ConnectorDialog from './ConnectorDialog.vue';
import AddConnectorDialog from './AddConnectorDialog.vue';
import OpenAlexDialog from './OpenAlexDialog.vue';
const catalogue = useConnectorLibrary(),
  custom = useWorkspaceMCPServers(),
  openalex = useOpenAlexConnector(),
  workspaces = useWorkspaces();
const query = ref(''),
  field = ref(''),
  selected = ref(''),
  kind = ref<'catalogue' | 'custom'>('catalogue'),
  showDetail = ref(false),
  showOpenAlex = ref(false),
  adding = ref(false);
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
    <p v-if="policy.error.value" class="inline-error" role="alert">{{ policy.error.value }}</p>
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
            @click="showOpenAlex = true"
          >
            <div>
              <strong>OpenAlex</strong>
              <p>Papers, preprints and abstracts across all fields, arXiv included. Built in.</p>
            </div>
          </button>
          <AvailabilitySwitch
            name="OpenAlex"
            :checked="openalex.data.value?.policy_enabled ?? false"
            :disabled="
              !openalex.data.value ||
              openalex.isError.value ||
              !policy.catalogueAllowed.value ||
              !!policy.pending.value
            "
            @toggle="
              policy.toggle(
                'catalogue',
                'openalex',
                openalex.data.value!.revision,
                !openalex.data.value!.policy_enabled,
              )
            "
          />
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
          <AvailabilitySwitch
            :name="row.connector.name"
            :checked="row.connector.policy_enabled"
            :disabled="!policy.catalogueAllowed.value || !!policy.pending.value"
            @toggle="
              policy.toggle(
                'catalogue',
                row.connector.name,
                row.connector.revision,
                !row.connector.policy_enabled,
              )
            "
          />
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
                >Custom · {{ row.connector.transport === 'url' ? 'Remote URL' : 'Command' }}</small
              >
            </div></button
          ><AvailabilitySwitch
            :name="row.connector.name"
            :checked="row.connector.policy_enabled ?? true"
            :disabled="!policy.allowed.value || !!policy.pending.value"
            @toggle="
              policy.toggle(
                'mcp',
                row.connector.id,
                row.connector.policy_revision ?? 0,
                !(row.connector.policy_enabled ?? true),
              )
            "
          />
        </div> </template></CapabilityList
    ><ConnectorDialog
      v-if="showDetail && selected"
      :key="workspaceId + kind + selected"
      :connector-id="selected"
      :kind="kind"
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
  font-size: 12px;
  color: var(--muted);
  line-height: 1.5;
  margin: 0 0 14px;
}
.budget {
  flex: none;
  font-size: 11px;
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
  font-size: 12px;
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  margin: 5px 0 0;
}
.connector-row small {
  font-size: 11px;
  margin-top: 6px;
}
button:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: 2px;
}
</style>
