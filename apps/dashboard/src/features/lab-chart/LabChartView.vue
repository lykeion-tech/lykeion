<script setup lang="ts">
import { useRouter } from 'vue-router';
import { useLabChart, useMe } from '../../api/queries';
import LabChartScene from './LabChartScene.vue';

const router = useRouter();
const chart = useLabChart();
const me = useMe();
</script>

<template>
  <div class="lab-chart-page">
    <LabChartScene
      v-if="chart.data.value"
      :chart="chart.data.value"
      :me="me.data.value?.id"
      @open-agent="(id) => router.push(`/agents/${id}`)"
      @open-person="(id) => router.push(`/colleagues/${id}`)"
    />
  </div>
</template>

<style scoped>
.lab-chart-page {
  flex: 1;
  min-height: 0;
  height: 100%;
}
</style>
