<script setup lang="ts">
import type { Cli } from './types';
defineProps<{ cli: Cli }>();
import { cliIconPaths } from '../../../../../packages/cli-icons';
</script>
<template>
  <svg
    viewBox="0 0 24 24"
    width="1em"
    height="1em"
    :fill="cli === 'claude' ? '#D97757' : 'currentColor'"
    aria-hidden="true"
  >
    <path :d="cliIconPaths[cli]" />
  </svg>
</template>
