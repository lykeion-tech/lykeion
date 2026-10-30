<script setup lang="ts">
import { computed } from 'vue';
import { useMe, useMembers, useRuntimes } from '../api/queries';
import { studyMachines } from '../lib/machines';
const value = defineModel<string>({ default: '' });
// A chip shows the select alone; its aria-label still names it.
defineProps<{ disabled?: boolean; chip?: boolean }>();
const me = useMe();
const members = useMembers();
const runtimes = useRuntimes();
const machines = computed(() => studyMachines(runtimes.data.value ?? [], me.data.value?.id ?? ''));
function label(owner: string) {
  const member = members.data.value?.find((m) => m.user_id === owner);
  return member?.name || member?.email || owner;
}
</script>
<template>
  <label class="machine-picker type-caption"
    ><template v-if="!chip">Machine</template>
    <select v-model="value" aria-label="Machine" :disabled="disabled">
      <option value="">My machines</option>
      <option v-for="machine in machines" :key="machine.id" :value="machine.id">
        {{
          machine.ownerId === me.data.value?.id
            ? machine.name
            : `${machine.name} (${label(machine.ownerId)})`
        }}
      </option>
      <option v-if="value && !machines.some((m) => m.id === value)" :value="value" disabled>
        Unavailable machine
      </option>
    </select>
  </label>
</template>
<style scoped>
.machine-picker {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: var(--muted);
}
select {
  max-width: 100%;
  color: var(--text);
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 4px 8px;
  font: inherit;
}
</style>
