<script setup lang="ts">
import Icon from '../../../ui/Icon.vue';
import type { ResearchProfile } from '../../../api/research-profile';

const profile = defineModel<ResearchProfile>('profile', { required: true });
defineProps<{ disabled?: boolean; hideBackgroundLabel?: boolean }>();

function setLink(index: number, value: string) {
  profile.value = {
    ...profile.value,
    links: profile.value.links.map((link, i) => (index === i ? value : link)),
  };
}
</script>

<template>
  <fieldset class="research-profile-fields" :disabled="disabled" aria-label="Research profile">
    <label class="profile-field">
      <span v-if="!hideBackgroundLabel">Professional background</span>
      <textarea
        :aria-label="hideBackgroundLabel ? 'Professional background' : undefined"
        :value="profile.identity"
        rows="8"
        maxlength="4000"
        placeholder="Describe your research, the methods you use, or the work you’d like to collaborate on."
        @input="profile = { ...profile, identity: ($event.target as HTMLTextAreaElement).value }"
      />
    </label>
    <div class="profile-field">
      <span>Links to your work (optional)</span>
      <div v-for="(link, index) in profile.links" :key="index" class="profile-link-field">
        <input
          :value="link"
          :aria-label="`Link ${index + 1}`"
          type="url"
          inputmode="url"
          placeholder="https://"
          maxlength="512"
          @input="setLink(index, ($event.target as HTMLInputElement).value)"
        />
        <button
          class="icon-button"
          type="button"
          :aria-label="`Remove link ${index + 1}`"
          @click="profile = { ...profile, links: profile.links.filter((_, i) => i !== index) }"
        >
          <Icon name="close" />
        </button>
      </div>
      <button
        v-if="profile.links.length < 5"
        class="button"
        type="button"
        @click="profile = { ...profile, links: [...profile.links, ''] }"
      >
        <Icon name="plus" />Add link
      </button>
    </div>
  </fieldset>
</template>

<style scoped>
.research-profile-fields {
  display: grid;
  gap: 16px;
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}
.profile-field {
  display: grid;
  min-width: 0;
  gap: 7px;
  font-size: 13px;
}
.profile-field > span {
  font-weight: 500;
}
.profile-field textarea,
.profile-field input {
  width: 100%;
  min-width: 0;
  box-sizing: border-box;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 7px;
  background: var(--input);
  color: inherit;
  font: inherit;
  line-height: 1.5;
}
.profile-field textarea {
  resize: vertical;
}
.profile-field > .button {
  justify-self: start;
}
.profile-link-field {
  display: flex;
  align-items: center;
  gap: 8px;
}
</style>
