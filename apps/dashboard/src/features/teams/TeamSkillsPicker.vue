<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useSkillLibrary } from '../../api/queries';
import type { TeamMember } from '../../api/types';
import Modal from '../../ui/Modal.vue';
const props = defineProps<{ member: TeamMember; busy: boolean; error: string }>();
const emit = defineEmits<{ close: []; save: [skills: string[]] }>();
const library = useSkillLibrary();
const selected = ref([...(props.member.skills ?? [])]);
const search = ref('');
watch(
  () => props.member,
  (member) => {
    selected.value = [...(member.skills ?? [])];
  },
);
const options = computed(() =>
  [
    ...new Set([
      ...(props.member.skills ?? []),
      ...(library.data.value ?? []).filter((s) => s.policy_enabled).map((s) => s.name),
    ]),
  ]
    .sort()
    .filter((name) => name.toLowerCase().includes(search.value.toLowerCase())),
);
const changed = computed(
  () =>
    JSON.stringify([...selected.value].sort()) !==
    JSON.stringify([...(props.member.skills ?? [])].sort()),
);
</script>
<template>
  <Modal :title="'Skills for ' + member.name" @close="!busy && emit('close')">
    <form @submit.prevent="emit('save', selected)">
      <h2>Skills for {{ member.name }}</h2>
      <p class="skill-help">
        Choose the skills this agent can use. Saving changes the Team, so it can't be saved while a
        study uses it.
      </p>
      <input
        v-model="search"
        type="search"
        aria-label="Search skills"
        placeholder="Search skills…"
      />
      <p v-if="library.isPending.value" role="status">Loading skills…</p>
      <p v-if="library.isError.value" role="alert">
        Could not load skills. <button type="button" @click="library.refetch()">Retry</button>
      </p>
      <fieldset :disabled="busy" aria-label="Skills" class="skill-options hide-scrollbar">
        <label v-for="name in options" :key="name"
          ><input v-model="selected" type="checkbox" :value="name" />{{ name }}</label
        >
        <p v-if="!options.length && library.isSuccess.value">No matching skills.</p>
      </fieldset>
      <p v-if="error" role="alert" class="inline-error">{{ error }}</p>
      <footer>
        <button type="button" class="button" :disabled="busy" @click="emit('close')">Cancel</button
        ><button class="button primary" :disabled="busy || !changed || !library.isSuccess.value">
          {{ busy ? 'Saving…' : 'Save' }}
        </button>
      </footer>
    </form>
  </Modal>
</template>
<style scoped>
form {
  padding: 24px;
}
h2 {
  font-size: 18px;
  margin: 0 0 12px;
}
.skill-help {
  color: var(--muted);
  font-size: 13px;
  line-height: 1.6;
  margin: 0 0 16px;
}
input[type='search'] {
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 6px;
  width: 100%;
  margin-bottom: 12px;
}
.skill-options {
  border: 0;
  padding: 0;
  max-height: 300px;
  overflow: auto;
  display: grid;
  gap: 4px;
}
.skill-options label {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px;
  font-size: 13px;
  border-radius: 4px;
  overflow-wrap: anywhere;
}
.skill-options label:hover {
  background: var(--hover);
}
.skill-options input {
  width: 16px;
  height: 16px;
  flex: none;
}
footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 20px;
}
</style>
