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
    <p v-if="chart.error.value" class="inline-error lab-chart-state" role="alert">
      {{ chart.error.value.message }}
      <button :disabled="chart.isFetching.value" @click="chart.refetch()">Retry</button>
    </p>
    <p v-else-if="chart.isPending.value" class="muted lab-chart-state" role="status">
      Loading Lykeion…
    </p>
    <LabChartScene
      v-else-if="chart.data.value"
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
}
.lab-chart-state {
  margin: 16px;
}
</style>
