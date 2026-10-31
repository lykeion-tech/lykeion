<script setup lang="ts">
defineProps<{ name: string }>();
</script>
<template>
  <svg
    width="16"
    height="16"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.75"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <template v-if="name === 'BarChart'">
      <path d="M4 20V10M10 20V4M16 20v-8M22 20H2" />
    </template>
    <template v-if="name === 'Clock'">
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 2" />
    </template>
    <template v-if="name === 'Chat'">
      <path d="M20 11.5a7.5 7.5 0 0 1-10.9 6.7L4 20l1.8-4.9A7.5 7.5 0 1 1 20 11.5z" />
    </template>
    <template v-if="name === 'Terminal'">
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="m7 9 3 3-3 3M13 15h4" />
    </template>
    <template v-if="name === 'Chip'">
      <rect x="6" y="6" width="12" height="12" rx="1.5" />
      <rect x="9" y="9" width="6" height="6" rx="1" />
      <path d="M9 2v3M15 2v3M9 19v3M15 19v3M2 9h3M2 15h3M19 9h3M19 15h3" />
    </template>
    <template v-if="name === 'Search'">
      <circle cx="11" cy="11" r="7" />
      <path d="m21 21-4.3-4.3" />
    </template>
    <template v-if="name === 'Trash'">
      <path d="M4 7h16" />
      <path d="M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2" />
      <path d="M6 7v12a2 2 0 0 0 2 2h8a2 2 0 0 0 2-2V7" />
      <path d="M10 11v6M14 11v6" />
    </template>
    <template v-if="name === 'Refresh'">
      <path d="M20 12a8 8 0 1 1-2.34-5.66" />
      <path d="M20 4v4.5h-4.5" />
    </template>
    <template v-if="name === 'Tools'">
      <rect x="3.5" y="3.5" width="7" height="7" rx="1.8" />
      <rect x="13.5" y="3.5" width="7" height="7" rx="1.8" />
      <rect x="3.5" y="13.5" width="7" height="7" rx="1.8" />
      <rect x="13.5" y="13.5" width="7" height="7" rx="1.8" />
    </template>
    <template v-if="name === 'Expand'">
      <path d="M8 3H3v5M16 3h5v5M21 16v5h-5M3 16v5h5" />
    </template>
    <template v-if="name === 'Download'">
      <path d="M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2" />
      <path d="M12 3v12M8 11l4 4 4-4" />
    </template>
    <template v-if="name === 'ChevronDown'">
      <path d="m6 9 6 6 6-6" />
    </template>
    <template v-if="name === 'ChevronLeft'">
      <path d="m15 6-6 6 6 6" />
    </template>
    <template v-if="name === 'ChevronRight'">
      <path d="m9 6 6 6-6 6" />
    </template>
    <template v-if="name === 'GitBranch'">
      <circle cx="6" cy="6" r="2.5" />
      <circle cx="6" cy="18" r="2.5" />
      <circle cx="18" cy="6" r="2.5" />
      <path d="M6 8.5v7M18 8.5a9 9 0 0 1-9 9" />
    </template>
  </svg>
</template>
