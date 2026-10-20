<script setup lang="ts">
import type { TeamMember } from '../api/types';
import { backendLabel } from '../lib/backends';
import Icon from './Icon.vue';
defineProps<{ member: TeamMember; role?: string; card?: boolean; editableSkills?: boolean }>();
const emit = defineEmits<{ editSkills: [] }>();
</script>
<template>
  <article :class="['team-member-profile', { 'role-card': card }]">
    <h3 v-if="card" class="role-kind">
      <Icon :name="role === 'Auditor' ? 'shield' : 'network'" :size="14" />{{ role }}
    </h3>
    <div class="member-identity">
      <span class="avatar">{{ member.name[0] }}</span>
      <div class="member-copy">
        <h4>
          {{ member.name }}<small v-if="!card && role" class="role-label">{{ role }}</small>
        </h4>
        <small v-if="card" class="member-backend">{{ backendLabel(member.agent.harness) }}</small>
        <p v-if="!card">{{ member.role }}</p>
      </div>
      <small v-if="!card" class="member-backend">{{ backendLabel(member.agent.harness) }}</small>
    </div>
    <p v-if="card" class="member-role">{{ member.role }}</p>
    <p v-if="member.skills?.length" class="member-capabilities">
      Skills: {{ member.skills.join(', ') }}
    </p>
    <button
      v-if="editableSkills"
      class="edit-skills"
      :aria-label="'Edit skills for ' + member.name"
      @click="emit('editSkills')"
    >
      <Icon name="plus" :size="12" />Edit skills
    </button>
    <p v-if="member.connectors?.length" class="member-capabilities">
      Connectors: {{ member.connectors.map((c) => c.catalogue || c.url || c.name).join(', ') }}
    </p>
  </article>
</template>
<style scoped>
.team-member-profile {
  padding: 16px 0;
  border-bottom: 1px solid var(--border);
}
.role-card {
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 18px;
}
.role-kind {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 11px;
  font-weight: 400;
  color: var(--muted);
  margin-bottom: 14px;
}
.member-identity {
  display: flex;
  align-items: center;
  gap: 12px;
}
.avatar {
  width: 32px;
  height: 32px;
  flex: none;
  font-size: 12px;
}
.member-copy {
  flex: 1;
  min-width: 0;
}
h4 {
  margin: 0;
  font-size: 14px;
  font-weight: 500;
  overflow-wrap: anywhere;
}
.member-backend {
  font-size: 12px;
  color: var(--muted);
}
.member-copy p,
.member-role {
  font-size: 13px;
  color: var(--muted);
  line-height: 1.6;
  overflow-wrap: anywhere;
}
.member-copy p {
  margin-top: 3px;
}
.member-role {
  margin-top: 12px;
}
.member-capabilities {
  font-size: 12px;
  color: var(--muted);
  margin: 8px 0 0 44px;
  overflow-wrap: anywhere;
}
.role-card .member-capabilities {
  margin-left: 0;
}
.edit-skills {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin: 8px 0 0 44px;
  font-size: 12px;
  color: var(--muted);
}
.edit-skills:hover {
  color: var(--text);
}
.role-label {
  margin-left: 8px;
  padding: 3px 6px;
  background: var(--input);
  color: var(--muted);
  border-radius: 4px;
  font-size: 11px;
  font-weight: 400;
}
@media (max-width: 600px) {
  .member-identity {
    flex-wrap: wrap;
  }
  .member-backend {
    overflow-wrap: anywhere;
  }
}
</style>
