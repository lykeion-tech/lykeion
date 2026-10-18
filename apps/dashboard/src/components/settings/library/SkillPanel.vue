<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { workspaceId } from '../../../api/session';
import { useSkillLibrary } from '../../../api/library';
import { useMe, useMembers } from '../../../api/queries';
import { matchesQuery } from '../../../lib/search';
import Icon from '../../Icon.vue';
import CapabilityList from './CapabilityList.vue';
import SkillDialog from './SkillDialog.vue';
import AddSkillDialog from './AddSkillDialog.vue';
const library = useSkillLibrary();
const me = useMe();
const members = useMembers();
const query = ref(''),
  selected = ref(''),
  adding = ref(false),
  showDetail = ref(false),
  editing = ref(false);
const rows = computed(() =>
  (library.data.value ?? []).filter((s) =>
    matchesQuery(`${s.name} ${s.description} ${s.domain}`, query.value),
  ),
);
watch(
  workspaceId,
  () => {
    selected.value = '';
    showDetail.value = false;
    adding.value = false;
    editing.value = false;
    query.value = '';
  },
  { flush: 'sync' },
);
function created(id: string) {
  adding.value = false;
  editing.value = true;
  selected.value = id;
  showDetail.value = true;
}
function author(id: string | null) {
  if (id === me.data.value?.id) return 'Added by you';
  const member = members.data.value?.find((m) => m.user_id === id);
  return member ? `Added by ${member.name || member.email}` : 'Workspace import';
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
    <CapabilityList
      v-model:query="query"
      :loading="library.isPending.value"
      :error="library.error.value?.message"
      :count="rows.length"
      @retry="library.refetch()"
    >
      <button
        v-for="s in rows"
        :key="s.id"
        class="skill-row"
        :class="{ chosen: selected === s.id }"
        :aria-label="s.name"
        @click="
          selected = s.id;
          showDetail = true;
          editing = false;
        "
      >
        <strong>{{ s.name }}</strong
        ><span>{{ s.description }}</span
        ><small
          >{{ s.origin === 'bundled' ? 'K-Dense · Read-only' : author(s.created_by)
          }}<template v-if="s.contains_scripts"> · Contains scripts</template></small
        >
      </button>
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
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 0 0 14px;
  min-height: 32px;
}
.library-heading h2 {
  font-size: 18px;
  font-weight: 600;
  margin: 0;
}
.button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.skill-row {
  display: block;
  width: 100%;
  text-align: left;
  background: transparent;
  color: var(--text);
  border: 0;
  border-bottom: 1px solid var(--border);
  padding: 14px 7px;
  cursor: pointer;
}
.skill-row:hover,
.skill-row.chosen {
  background: var(--hover);
  border-radius: 7px;
}
.skill-row strong {
  font-size: 13px;
  font-weight: 500;
}
.skill-row span,
.skill-row small {
  display: block;
  font-size: 12px;
  color: var(--muted);
  margin-top: 5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.skill-row small {
  font-size: 11px;
}
.skill-row:focus-visible {
  outline: 2px solid var(--primary);
  outline-offset: 2px;
}
</style>
