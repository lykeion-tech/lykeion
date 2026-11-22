<script setup lang="ts">
import { computed } from 'vue';
import type { ResearchProfile } from '../../api/research-profile';

const props = defineProps<{ profile: ResearchProfile }>();
const links = computed(() =>
  props.profile.links.filter((link) => {
    try {
      const url = new URL(link);
      return (
        (url.protocol === 'https:' || url.protocol === 'http:') && !url.username && !url.password
      );
    } catch {
      return false;
    }
  }),
);
</script>

<template>
  <div class="published-profile">
    <p v-if="profile.identity" class="profile-identity">{{ profile.identity }}</p>
    <template v-if="links.length">
      <h4>Links to work</h4>
      <ul>
        <li v-for="link in links" :key="link">
          <a :href="link" target="_blank" rel="noopener noreferrer">{{ link }}</a>
        </li>
      </ul>
    </template>
    <p v-if="!profile.identity && !links.length" class="muted">No research details provided.</p>
  </div>
</template>

<style scoped>
.published-profile {
  overflow-wrap: anywhere;
  font-size: 13.5px;
  line-height: 1.55;
}
.published-profile p,
.published-profile ul {
  margin: 0;
}
.published-profile h4 {
  margin: 0 0 4px;
  font-size: 13px;
  font-weight: 600;
}
.profile-identity {
  white-space: pre-wrap;
}
.profile-identity + h4 {
  margin-top: 14px;
}
.published-profile a {
  color: inherit;
  text-decoration: underline;
  text-underline-offset: 3px;
}
</style>
