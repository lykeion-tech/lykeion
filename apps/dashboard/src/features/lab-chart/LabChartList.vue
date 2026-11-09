<script setup lang="ts">
import type { LabChart, LabChartAgent } from '../../api/types';
import { STAGE_LABEL } from './scene';

defineProps<{ chart: LabChart; preview?: boolean; hidden?: boolean }>();
const emit = defineEmits<{ 'open-agent': [id: string]; 'open-person': [userId: string] }>();
const agent = (id: string) => emit('open-agent', id);
const person = (id: string) => emit('open-person', id);
const STATUS: Record<LabChartAgent['status'], string> = {
  idle: 'Idle',
  working: 'Working',
  needs_you: 'Needs you',
};
</script>

<template>
  <section :class="['lab-chart-list', { 'is-hidden': hidden }]" aria-label="Lykeion rooms">
    <div v-for="room in chart.rooms" :key="room.stage" class="lab-chart-list-room">
      <h3>{{ STAGE_LABEL[room.stage] }}</h3>
      <ul>
        <li v-for="a in room.agents" :key="a.id">
          <button v-if="!preview" type="button" @click="agent(a.id)">{{ a.name }}</button>
          <span v-else>{{ a.name }}</span
          >{{ ` · ${a.role} · ${STATUS[a.status]}` }}
        </li>
      </ul>
    </div>
    <div class="lab-chart-list-room">
      <h3>Conference room</h3>
      <ul>
        <li v-for="p in chart.people" :key="p.user_id">
          <button v-if="!preview" type="button" @click="person(p.user_id)">{{ p.name }}</button>
          <span v-else>{{ p.name }}</span
          >{{ ` · ${p.role}` }}
        </li>
      </ul>
    </div>
  </section>
</template>
