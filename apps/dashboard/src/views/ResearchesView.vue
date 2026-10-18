<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue';
import { useQueryClient } from '@tanstack/vue-query';
import { useRouter } from 'vue-router';
import { api } from '../api/client';
import { keys } from '../api/keys';
import { workspaceId } from '../api/session';
import { useResearch, useResearches } from '../api/queries';
import type { Research } from '../api/types';
import Modal from '../components/Modal.vue';
import Icon from '../components/Icon.vue';
const props = defineProps<{ id?: string }>();
const list = useResearches(),
  detail = useResearch(() => props.id ?? '');
const client = useQueryClient(),
  router = useRouter();
const editor = ref(false),
  name = ref(''),
  description = ref(''),
  busy = ref(false),
  error = ref('');
let visit = 0;
watch(
  [workspaceId, () => props.id],
  () => {
    visit++;
    editor.value = false;
    error.value = '';
    busy.value = false;
  },
  { flush: 'sync' },
);
onUnmounted(() => visit++);
const failure = computed(() => (props.id ? detail.error.value : list.error.value));
function edit() {
  name.value = detail.data.value?.name ?? '';
  description.value = detail.data.value?.description ?? '';
  error.value = '';
  editor.value = true;
}
async function save() {
  const ws = workspaceId.value,
    id = props.id,
    v = visit;
  busy.value = true;
  error.value = '';
  try {
    const saved = await api<Research>(
      id ? 'PATCH' : 'POST',
      id ? `/api/researches/${id}` : '/api/researches',
      { name: name.value, description: description.value },
    );
    if (v !== visit || ws !== workspaceId.value) return;
    editor.value = false;
    await client.invalidateQueries({ queryKey: keys.researches(ws) });
    if (v !== visit || ws !== workspaceId.value) return;
    if (id) await client.invalidateQueries({ queryKey: keys.research(ws, id) });
    else await router.push('/projects/' + saved.id);
  } catch (e) {
    if (v === visit) error.value = (e as Error).message;
  } finally {
    if (v === visit) busy.value = false;
  }
}
</script>
<template>
  <section class="directory-page">
    <template v-if="id"
      ><RouterLink to="/projects" class="text-button">← Projects</RouterLink>
      <div v-if="detail.data.value" class="research-detail">
        <header>
          <h1>{{ detail.data.value.name }}</h1>
          <button class="button" @click="edit">Edit Project</button>
        </header>
        <p class="description">{{ detail.data.value.description || 'No description.' }}</p>
        <h2>Studies</h2>
        <div class="research-studies">
          <RouterLink
            v-for="study in detail.data.value.studies"
            :key="study.id"
            :to="'/studies/' + study.id"
            ><Icon name="tasks" /><span
              >{{ study.title
              }}<small>{{ study.board_column }} · round {{ study.round }}</small></span
            ><Icon name="right"
          /></RouterLink>
          <p v-if="!detail.data.value.studies.length" class="muted">
            No studies linked yet. Choose this Project in a study’s properties.
          </p>
        </div>
      </div></template
    ><template v-else
      ><Teleport to="#page-header-actions">
        <button
          class="button primary"
          @click="
            name = '';
            description = '';
            editor = true;
          "
        >
          <Icon name="plus" :size="14" />New Project
        </button>
      </Teleport>
      <div class="directory-list">
        <RouterLink v-for="r in list.data.value ?? []" :key="r.id" :to="'/projects/' + r.id"
          ><Icon name="folder" /><span
            >{{ r.name }}<small>{{ r.description || 'No description' }}</small></span
          ><Icon name="right"
        /></RouterLink>
      </div>
      <div v-if="list.isSuccess.value && !list.data.value?.length" class="directory-empty">
        <Icon name="folder" :size="30" />
        <h2>No Projects yet</h2>
        <p>Group related studies in a Project.</p>
        <button class="button" @click="edit">Create Project</button>
      </div></template
    >
    <p v-if="failure" class="inline-error" role="alert">
      {{ failure.message }} <button @click="id ? detail.refetch() : list.refetch()">Retry</button>
    </p>
    <p v-if="id ? detail.isPending.value : list.isPending.value" role="status">Loading Projects…</p>
    <Modal v-if="editor" :title="id ? 'Edit Project' : 'New Project'" @close="editor = false"
      ><form class="directory-form" @submit.prevent="save">
        <h2>{{ id ? 'Edit Project' : 'New Project' }}</h2>
        <label>Name<input v-model="name" required aria-label="Project name" /></label
        ><label
          >Description<textarea v-model="description" aria-label="Project description" />
        </label>
        <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
        <footer>
          <button type="button" class="button" @click="editor = false">Cancel</button
          ><button class="button primary" :disabled="busy || !name.trim()">
            {{ id ? 'Save Project' : 'Create Project' }}
          </button>
        </footer>
      </form></Modal
    >
  </section>
</template>
<style src="./directory.css" />
