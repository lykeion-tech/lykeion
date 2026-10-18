<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { workspaceId } from '../../../api/session';
import { useConnectorLibrary, useWorkspaceMCPServers } from '../../../api/library';
import { useWorkspaces } from '../../../api/queries';
import { matchesQuery } from '../../../lib/search';
import Icon from '../../Icon.vue';
import AvailabilitySwitch from './AvailabilitySwitch.vue';
import { useAvailabilityChanges } from '../../../state/availability';
const policy = useAvailabilityChanges();
import CapabilityList from './CapabilityList.vue';
import ConnectorDialog from './ConnectorDialog.vue';
import AddConnectorDialog from './AddConnectorDialog.vue';
const catalogue = useConnectorLibrary(),
  custom = useWorkspaceMCPServers(),
  workspaces = useWorkspaces();
const query = ref(''),
  selected = ref(''),
  kind = ref<'catalogue' | 'custom'>('catalogue'),
  showDetail = ref(false),
  adding = ref(false);
const allowed = computed(() =>
  ['owner', 'admin'].includes(
    workspaces.data.value?.find((w) => w.id === workspaceId.value)?.role ?? '',
  ),
);
const scientific = computed(() =>
  (catalogue.data.value ?? []).filter((c) =>
    matchesQuery(`${c.name} ${c.domain} ${c.purpose}`, query.value),
  ),
);
const own = computed(() =>
  (custom.data.value ?? []).filter((c) => matchesQuery(`${c.name} ${c.transport}`, query.value)),
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
    query.value = '';
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
      noun="connectors"
      :loading="catalogue.isPending.value || custom.isPending.value"
      :error="catalogue.error.value?.message || custom.error.value?.message"
      :count="scientific.length + own.length"
      @retry="
        catalogue.refetch();
        custom.refetch();
      "
      ><h3 v-if="scientific.length">Scientific databases</h3>
      <p v-if="scientific.length" class="section-note">Available to agents in this workspace.</p>
      <div v-for="c in scientific" :key="'catalogue:' + c.name" class="connector-entry">
        <button
          class="connector-row"
          :class="{ chosen: selected === c.name && kind === 'catalogue' }"
          :aria-label="c.name"
          @click="open(c.name, 'catalogue')"
        >
          <div>
            <strong>{{ c.name }}</strong>
            <p>{{ c.purpose }}</p>
            <small>{{ c.domain }} · {{ c.tier }} · {{ c.enabled ? 'Available' : c.reason }}</small>
          </div>
        </button>
        <AvailabilitySwitch
          :name="c.name"
          :checked="c.policy_enabled"
          :disabled="!policy.catalogueAllowed.value || !!policy.pending.value"
          @toggle="policy.toggle('catalogue', c.name, c.revision, !c.policy_enabled)"
        />
      </div>
      <h3 v-if="own.length">Custom</h3>
      <p v-if="own.length" class="section-note">Shared connections added to this workspace.</p>
      <div v-for="c in own" :key="'custom:' + c.id" class="connector-entry">
        <button
          class="connector-row"
          :class="{ chosen: selected === c.id && kind === 'custom' }"
          :aria-label="c.name"
          @click="open(c.id, 'custom')"
        >
          <div>
            <strong>{{ c.name }}</strong
            ><small>Custom · {{ c.transport === 'url' ? 'Remote URL' : 'Command' }}</small>
          </div></button
        ><AvailabilitySwitch
          :name="c.name"
          :checked="c.policy_enabled ?? true"
          :disabled="!policy.allowed.value || !!policy.pending.value"
          @toggle="policy.toggle('mcp', c.id, c.policy_revision ?? 0, !(c.policy_enabled ?? true))"
        /></div></CapabilityList
    ><ConnectorDialog
      v-if="showDetail && selected"
      :key="workspaceId + kind + selected"
      :connector-id="selected"
      :kind="kind"
      @close="showDetail = false"
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
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 0 0 14px;
  min-height: 32px;
}
.heading h2 {
  font-size: 18px;
  font-weight: 600;
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
.connector-row:hover,
.connector-row.chosen {
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
  outline: 2px solid var(--primary);
  outline-offset: 2px;
}
</style>
