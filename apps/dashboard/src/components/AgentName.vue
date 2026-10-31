<script setup lang="ts">
import { computed } from 'vue';
import CliIcon from '../coresearch/CliIcon.vue';
import { backendLabel } from '../lib/backends';
import { agentDisplayName, agentNameWithoutCli } from '../lib/agent-name';
const props = defineProps<{ name: string; harness: string }>();
const label = computed(() => agentNameWithoutCli(props.name, props.harness));
const replaced = computed(() => label.value !== agentDisplayName(props.name));
</script>
<template>
  <span class="agent-name"
    >{{ label
    }}<span
      v-if="replaced && (harness === 'claude' || harness === 'codex')"
      class="agent-cli"
      role="img"
      :aria-label="backendLabel(harness)"
      :title="backendLabel(harness)"
      ><CliIcon :cli="harness" /></span
  ></span>
</template>
<style scoped>
.agent-name {
  display: inline;
  overflow-wrap: anywhere;
}
.agent-cli {
  display: inline-flex;
  vertical-align: -0.1em;
  margin-left: 0.35em;
}
</style>
