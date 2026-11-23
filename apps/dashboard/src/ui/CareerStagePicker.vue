<script setup lang="ts">
import SelectPicker from './SelectPicker.vue';
import type { CareerStage } from '../api/types';
import { careerStageChoices, careerStageLabels, selectableCareerStage } from '../lib/studies';

const props = defineProps<{ modelValue?: CareerStage; disabled?: boolean }>();
const emit = defineEmits<{ 'update:modelValue': [stage: CareerStage] }>();

function change(event: Event) {
  const select = event.target as HTMLSelectElement;
  const stage = selectableCareerStage(select.value);
  select.value = selectableCareerStage(props.modelValue);
  emit('update:modelValue', stage);
}
</script>

<template>
  <SelectPicker>
    <select
      aria-label="Your career stage"
      class="career-stage-select"
      :value="selectableCareerStage(modelValue)"
      :disabled="disabled"
      @change="change"
    >
      <option value="">Choose a stage</option>
      <option v-for="stage in careerStageChoices" :key="stage" :value="stage">
        {{ careerStageLabels[stage] }}
      </option>
    </select>
  </SelectPicker>
</template>
