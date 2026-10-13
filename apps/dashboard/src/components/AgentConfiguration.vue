<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRouter } from 'vue-router';
import type { Agent } from '../../../../packages/contracts/src/research';
import { state, service, refresh, notify } from '../services/workspace';
import Icon from './Icon.vue';
import AvatarPicker from './AvatarPicker.vue';
import AvatarDisplay from './AvatarDisplay.vue';
import ExecutionOptions from './ExecutionOptions.vue';
const props = defineProps<{ agent?: Agent; section?: 'general' | 'access' }>();
const emit = defineEmits<{ saved: [Agent] }>();
const router = useRouter();
const name = ref(props.agent?.name ?? ''),
  description = ref(props.agent?.description ?? props.agent?.role ?? ''),
  instructions = ref(props.agent?.instructions ?? ''),
  runtimeId = ref(props.agent?.runtimeId ?? ''),
  model = ref(props.agent?.model ?? ''),
  access = ref<NonNullable<Agent['access']>>(props.agent?.access ?? 'personal'),
  memberIds = ref([...(props.agent?.memberIds ?? [])]),
  suggestions = ref<{ label: string; prompt: string }[]>(
    props.agent?.conversationStarters?.map((x) => ({ ...x })) ??
      props.agent?.suggestions?.map((x) => ({ label: x, prompt: x })) ??
      [],
  ),
  thinking = ref(props.agent?.thinking ?? ''),
  skillIds = ref<string[]>([...(props.agent?.skillIds ?? [])]),
  avatar = ref(props.agent?.avatar ?? 'bot'),
  avatarMenu = ref(false),
  skillMenu = ref(false),
  error = ref(''),
  busy = ref(false);
const defaults = [
  'Ground a research question',
  'Design a reproducible experiment',
  'Review evidence and findings',
];
const validSuggestions = computed(() =>
  suggestions.value.filter((x) => x.label.trim() && x.prompt.trim()),
);
const preview = computed(() =>
  validSuggestions.value.length ? validSuggestions.value.map((x) => x.label) : defaults,
);
const incompleteSuggestions = computed(() =>
  suggestions.value.some((x) => !x.label.trim() || !x.prompt.trim()),
);
async function save() {
  busy.value = true;
  error.value = '';
  try {
    const input = {
      name: name.value,
      role: description.value,
      description: description.value,
      instructions: instructions.value,
      runtimeId: runtimeId.value || null,
      model: model.value,
      access: access.value,
      memberIds: memberIds.value,
      suggestions: validSuggestions.value.map((x) => x.prompt),
      conversationStarters: validSuggestions.value,
      thinking: thinking.value,
      skillIds: skillIds.value,
      avatar: avatar.value,
    };
    const agent = props.agent
      ? await service.value.updateResource('agents', props.agent.id, input)
      : await service.value.createResource('agents', input);
    await refresh();
    notify(
      props.agent
        ? 'Agent configuration saved.'
        : service.value.mode === 'local'
          ? 'Agent saved as an offline draft.'
          : 'Agent created.',
    );
    emit('saved', agent);
    if (!props.agent) await router.push('/agents/' + agent.id);
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <form class="agent-configuration" :class="{ embedded: section }" @submit.prevent="save">
    <section v-if="!section || section === 'general'">
      <h3>{{ section ? 'Profile' : 'Identity' }}</h3>
      <div class="configuration-box identity-box">
        <div class="avatar-row">
          <span>Change avatar</span>
          <div class="avatar-wrap">
            <button
              type="button"
              class="agent-avatar"
              aria-label="Change avatar"
              @click="avatarMenu = !avatarMenu"
            >
              <AvatarDisplay :value="avatar" :size="28" />
            </button>
            <AvatarPicker v-if="avatarMenu" v-model="avatar" @close="avatarMenu = false" />
          </div>
        </div>
        <label class="identity-row">
          <span>Name</span>
          <input v-model="name" autofocus placeholder="e.g. Deep Research Agent" required />
        </label>
        <label class="identity-row description-row">
          <span>Description</span>
          <div>
            <textarea
              v-model="description"
              maxlength="255"
              placeholder="What does this agent do?"
            />
            <small class="character-count">{{ description.length }} / 255</small>
          </div>
        </label>
      </div>
    </section>
    <section v-if="!section">
      <h3>Behavior &amp; capabilities</h3>
      <div class="configuration-box">
        <label class="instructions-section"
          >Instructions<textarea
            v-model="instructions"
            class="instructions-field"
            placeholder="Write what this agent should do, what to focus on, what to avoid…"
          />
        </label>
        <div class="starters-section">
          <label
            >Conversation starters<small
              >Up to three. Selecting one fills the composer without sending.</small
            >
          </label>
          <div v-for="(suggestion, i) in suggestions" :key="i" class="suggestion-field">
            <input
              v-model="suggestion.label"
              :aria-label="`Suggestion ${i + 1} label`"
              placeholder="Review a research question"
            />
            <button
              type="button"
              class="icon-button"
              :aria-label="`Remove suggestion ${i + 1}`"
              @click="suggestions.splice(i, 1)"
            >
              <Icon name="close" />
            </button>
            <textarea
              v-model="suggestion.prompt"
              :aria-label="`Suggestion ${i + 1} prompt`"
              placeholder="Review the research question, summarize the evidence, and recommend next steps."
            />
          </div>
          <button
            type="button"
            class="button compact"
            :disabled="suggestions.length >= 3"
            @click="suggestions.push({ label: '', prompt: '' })"
          >
            <Icon name="plus" :size="14" />Add suggestion
          </button>
          <small v-if="incompleteSuggestions"
            >Complete or remove each suggestion before saving.</small
          >
          <div class="chat-preview">
            <small>Preview of a new chat</small>
            <div>
              <button
                v-for="text in preview"
                :key="text"
                type="button"
                @click="notify('Preview: ' + text)"
              >
                {{ text }}
              </button>
            </div>
            <small v-if="!validSuggestions.length"
              >These are the built-in defaults. Add a suggestion above to replace all three.</small
            >
          </div>
        </div>
        <div class="skills-section">
          <div class="skills-heading">
            <small>SKILLS</small>
            <button type="button" class="text-button" @click="skillMenu = !skillMenu">
              <Icon name="plus" :size="14" />Add skills from workspace
            </button>
          </div>
          <div v-if="skillMenu" class="skill-chooser">
            <label v-for="skill in state.data?.skills" :key="skill.id">
              <input v-model="skillIds" type="checkbox" :value="skill.id" />{{ skill.name }}</label
            >
            <p v-if="!state.data?.skills.length">No workspace skills yet.</p>
          </div>
          <div v-if="skillIds.length" class="assigned-skills">
            <span v-for="id in skillIds" :key="id"
              >{{ state.data?.skills.find((x) => x.id === id)?.name ?? id
              }}<button
                type="button"
                :aria-label="'Remove skill ' + id"
                @click="skillIds = skillIds.filter((x) => x !== id)"
              >
                <Icon name="close" :size="12" />
              </button>
            </span>
          </div>
        </div>
      </div>
    </section>
    <section v-if="!section || section === 'general'">
      <h3>Execution</h3>
      <p>Unspecified options use the runtime defaults.</p>
      <ExecutionOptions
        v-model:runtime-id="runtimeId"
        v-model:model="model"
        v-model:thinking="thinking"
        :runtimes="state.data?.runtimes ?? []"
        @refresh-models="
          notify(
            'The runtime has not supplied a model catalog. Enter a model ID or use the provider default.',
          )
        "
      />
    </section>
    <section v-if="!section || section === 'access'">
      <h3>{{ section ? 'Who can run this agent' : 'Access' }}</h3>
      <fieldset class="configuration-box">
        <legend class="sr-only">Access</legend>
        <label
          v-for="option in [
            { value: 'personal', name: 'Only me', text: 'Only you can run this agent.' },
            {
              value: 'workspace',
              name: 'Entire workspace',
              text: 'Every workspace member can run it.',
            },
            {
              value: 'specific',
              name: 'Specific people',
              text: 'Only selected members can run it.',
            },
          ]"
          :key="option.value"
          class="access-option"
        >
          <input v-model="access" type="radio" :value="option.value" name="agent-access" />
          <span
            >{{ option.name }}<small>{{ option.text }}</small>
          </span>
        </label>
        <div v-if="access === 'specific'" class="member-chooser">
          <label v-for="member in state.data?.colleagues" :key="member.id">
            <input v-model="memberIds" type="checkbox" :value="member.id" />{{ member.name }}</label
          >
          <small v-if="!state.data?.colleagues.length">No workspace members available.</small>
        </div>
      </fieldset>
    </section>
    <p v-if="error" role="alert" class="configuration-error">{{ error }}</p>
    <div class="configuration-footer" :class="{ editing: agent }">
      <button class="button primary" :disabled="busy || !name.trim() || incompleteSuggestions">
        {{ busy ? 'Saving…' : agent ? 'Save changes' : 'Create & open agent' }}
      </button>
    </div>
  </form>
</template>
<style scoped>
.agent-configuration {
  width: min(832px, calc(100% - 48px));
  margin: 0 auto;
  padding: 30px 0 90px;
  display: flex;
  flex-direction: column;
  gap: 32px;
}
.agent-configuration > section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.agent-configuration h3 {
  font-size: 14px;
  font-weight: 500;
  line-height: 20px;
}
.configuration-box {
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--input);
  overflow: visible;
}
.agent-configuration label {
  font-size: 14px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  color: var(--text);
}
.agent-configuration input:not([type='radio']):not([type='checkbox']),
.agent-configuration textarea,
.agent-configuration select {
  border: 1px solid var(--border);
  border-radius: 6px;
  background: transparent;
  padding: 7px 10px;
  width: 100%;
  font-size: 14px;
  min-width: 0;
}
.agent-configuration input:not([type='radio']):not([type='checkbox']),
.agent-configuration select {
  height: 32px;
}
.agent-configuration textarea {
  resize: vertical;
  min-height: 64px;
}
.agent-configuration input[type='checkbox'],
.agent-configuration input[type='radio'] {
  accent-color: var(--primary);
  margin: 0;
}
.agent-configuration small,
.agent-configuration > section > p {
  font-size: 12px;
  line-height: 18px;
  color: var(--muted);
  font-weight: 400;
}
.agent-configuration .identity-row {
  display: grid;
  grid-template-columns: 1fr 1.14fr;
  align-items: center;
  padding: 16px;
  border-top: 1px solid var(--border);
  gap: 14px;
  min-height: 64px;
}
.agent-configuration .description-row {
  align-items: start;
}
.description-row > span {
  padding-top: 2px;
}
.description-row > div {
  display: flex;
  flex-direction: column;
}
.avatar-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px;
  min-height: 88px;
}
.avatar-wrap {
  position: relative;
}
.agent-avatar img {
  width: 100%;
  height: 100%;
  border-radius: 50%;
  object-fit: cover;
}
.avatar-emoji {
  font-size: 32px;
}
.agent-avatar {
  display: grid;
  place-items: center;
  width: 56px;
  height: 56px;
  background: #dfdfe6;
  border-radius: 50%;
}
.avatar-menu {
  position: absolute;
  z-index: 2;
  right: 0;
  top: 64px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: 0 5px 20px #0002;
  padding: 8px;
  display: grid;
  grid-template-columns: repeat(3, 36px);
}
.avatar-menu button {
  height: 36px;
  border-radius: 4px;
}
.avatar-menu button:hover {
  background: var(--input);
}
.character-count {
  text-align: right;
  margin-top: 0;
}
.instructions-section {
  padding: 16px;
}
.agent-configuration .instructions-field {
  height: 176px;
  font-family: ui-monospace, SFMono-Regular, monospace;
  line-height: 22px;
}
.starters-section {
  padding: 16px;
  border-top: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.starters-section label {
  gap: 6px;
}
.agent-configuration .button {
  align-self: flex-start;
}
.suggestion-field {
  display: grid;
  grid-template-columns: 1fr 28px;
  gap: 8px;
  align-items: center;
  background: #e8e8ee;
  border-radius: 7px;
  padding: 10px;
}
.suggestion-field > textarea {
  grid-column: 1/-1;
}
.chat-preview {
  padding: 12px;
  border: 1px solid var(--border);
  background: #e8e8ee;
  border-radius: 8px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.chat-preview > div {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-width: 400px;
  margin: 0 auto;
  width: 100%;
}
.chat-preview button {
  padding: 10px 12px;
  text-align: left;
  font-size: 13px;
  background: #e0e0e7;
  border-radius: 6px;
}
.skills-section {
  padding: 16px;
  border-top: 1px solid var(--border);
}
.skills-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.skills-heading > .text-button {
  display: flex;
  align-items: center;
  gap: 6px;
}
.skill-chooser,
.member-chooser {
  padding: 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--surface);
  display: grid;
  gap: 10px;
}
.skill-chooser > label,
.member-chooser > label {
  flex-direction: row;
  align-items: center;
}
.skill-chooser > p {
  font-size: 12px;
  color: var(--muted);
}
.assigned-skills {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 12px;
}
.assigned-skills > span {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 8px;
  border: 1px solid #fff7;
  border-radius: 5px;
  font-size: 12px;
}
.agent-configuration fieldset {
  padding: 0;
  margin: 0;
}
.agent-configuration .access-option {
  flex-direction: row;
  align-items: center;
  gap: 12px;
  padding: 16px;
  border-bottom: 1px solid var(--border);
}
.access-option small {
  display: block;
  margin-top: 3px;
}
.configuration-footer {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  height: 58px;
  background: var(--surface);
  border-top: 1px solid var(--border);
  display: flex;
  align-items: center;
  justify-content: flex-end;
  padding: 0 max(24px, calc((100% - 1416px) / 2));
  z-index: 5;
}
.agent-configuration .configuration-footer .button {
  align-self: center;
}
.configuration-footer.editing {
  position: sticky;
  bottom: -90px;
  padding: 12px 0;
  background: var(--surface);
}
.configuration-error {
  color: #9c3b3b;
  font-size: 13px;
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
}

.agent-configuration.embedded {
  width: 100%;
  margin: 0;
  padding: 0;
  gap: 32px;
}
.embedded .configuration-footer {
  position: static;
  height: auto;
  border: 0;
  padding: 0;
  justify-content: flex-end;
}
.embedded .identity-row {
  grid-template-columns: 1fr 1.3fr;
}
</style>
