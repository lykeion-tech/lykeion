<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { workspaceId } from '../../../api/session';
import { useSkillLibrary } from '../../../api/library';
import { matchesQuery } from '../../../lib/search';
import Icon from '../../../ui/Icon.vue';
import AvailabilitySwitch from './AvailabilitySwitch.vue';
import { useAvailabilityChanges } from '../../../state/availability';
const policy = useAvailabilityChanges();
import CapabilityList from './CapabilityList.vue';
import SkillDialog from './SkillDialog.vue';
import AddSkillDialog from './AddSkillDialog.vue';
const library = useSkillLibrary();
const query = ref(''),
  field = ref(''),
  selected = ref(''),
  adding = ref(false),
  showDetail = ref(false),
  editing = ref(false);
const fields = computed(() =>
  [
    ...new Set(
      (library.data.value ?? [])
        .map((s) => (s.origin === 'imported' ? 'Workspace' : s.domain))
        .filter(Boolean),
    ),
  ].sort(),
);
const rows = computed(() =>
  (library.data.value ?? [])
    .filter(
      (s) =>
        matchesQuery(`${s.name} ${s.description} ${s.domain}`, query.value) &&
        (!field.value || (s.origin === 'imported' ? 'Workspace' : s.domain) === field.value),
    )
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' })),
);
watch(
  workspaceId,
  () => {
    selected.value = '';
    showDetail.value = false;
    adding.value = false;
    editing.value = false;
    query.value = '';
    field.value = '';
  },
  { flush: 'sync' },
);
function created(id: string) {
  adding.value = false;
  editing.value = true;
  selected.value = id;
  showDetail.value = true;
}
</script>
<template>
  <section class="skill-panel">
    <div class="library-heading">
      <h2>Skills</h2>
      <button class="button primary" @click="adding = true">
        <Icon name="plus" :size="14" />Add skill
      </button>
    </div>
    <p v-if="policy.error.value" class="inline-error" role="alert">{{ policy.error.value }}</p>
    <CapabilityList
      v-model:query="query"
      v-model:field="field"
      :fields="fields"
      :loading="library.isPending.value"
      :error="library.error.value?.message"
      :count="rows.length"
      @retry="library.refetch()"
    >
      <div v-for="s in rows" :key="s.id" class="skill-entry">
        <button
          class="skill-row"
          :class="{ chosen: showDetail && selected === s.id }"
          :aria-label="s.name"
          @click="
            selected = s.id;
            showDetail = true;
            editing = false;
          "
        >
          <strong>{{ s.name }}</strong
          ><span>{{ s.description }}</span>
        </button>
        <AvailabilitySwitch
          :name="s.name"
          :checked="s.policy_enabled ?? true"
          :disabled="!policy.allowed.value || !!policy.pending.value"
          @toggle="
            policy.toggle('skill', s.id, s.policy_revision ?? 0, !(s.policy_enabled ?? true))
          "
        />
      </div>
    </CapabilityList>
    <SkillDialog
      v-if="selected && showDetail"
      :key="workspaceId + selected"
      :skill-id="selected"
      :start-edit="editing"
      @close="showDetail = false"
    />
    <AddSkillDialog v-if="adding" :key="workspaceId" @close="adding = false" @created="created" />
  </section>
</template>
<style scoped>
.library-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin: 0 0 14px;
  min-height: 32px;
}
.library-heading h2 {
  font-size: 20px;
  font-weight: 600;
  line-height: 28px;
  margin: 0;
}
.button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.skill-entry {
  display: flex;
  align-items: center;
  border-bottom: 1px solid var(--border);
}
.skill-row {
  flex: 1;
  min-width: 0;
  display: block;
  width: 100%;
  text-align: left;
  background: transparent;
  color: var(--text);
  border: 0;
  padding: 14px 7px;
  cursor: pointer;
}
.skill-entry:hover,
.skill-entry:has(.skill-row.chosen) {
  background: var(--hover);
  border-radius: 7px;
}
.skill-row strong {
  font-size: 13px;
  font-weight: 500;
}
.skill-row span {
  display: block;
  font-size: 12px;
  color: var(--muted);
  margin-top: 5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.skill-row:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: 2px;
}
</style>
