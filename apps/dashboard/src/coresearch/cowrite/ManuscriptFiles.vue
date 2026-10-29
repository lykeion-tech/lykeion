<script setup lang="ts">
import { computed, ref } from 'vue';
import type { FileRow } from '../types';
import CoresearchModal from '../CoresearchModal.vue';
import InlineRename from './InlineRename.vue';
import FileMenu from './FileMenu.vue';
const props = defineProps<{
  files: FileRow[];
  activePath?: string;
  namingNew?: boolean;
  remove?: (path: string) => Promise<void>;
}>();
const emit = defineEmits<{
  open: [string];
  newFile: [string];
  namingNewDone: [];
  rename: [string, string];
}>();
const naming = ref<string | null>(null),
  removing = ref<string | null>(null),
  busy = ref(false),
  error = ref('');
const folderOf = (path: string) => (path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '');
const groups = computed(() =>
  [
    '',
    ...new Set(
      props.files
        .map((f) => folderOf(f.path))
        .filter(Boolean)
        .sort(),
    ),
  ].map((folder) => ({
    folder,
    files: props.files
      .filter((f) => folderOf(f.path) === folder)
      .sort(
        (a, b) =>
          Number(b.path === 'main.tex') - Number(a.path === 'main.tex') ||
          a.path.localeCompare(b.path),
      ),
  })),
);
async function confirmRemove() {
  if (busy.value || !removing.value) return;
  busy.value = true;
  error.value = '';
  try {
    await props.remove?.(removing.value);
    removing.value = null;
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <div class="ms-files">
    <div v-if="namingNew" class="tsb-file">
      <InlineRename
        title=""
        label="New file"
        @commit="
          emit('namingNewDone');
          emit('newFile', $event);
        "
        @cancel="emit('namingNewDone')"
      />
    </div>
    <template v-for="group in groups" :key="group.folder"
      ><div :class="group.folder ? 'tsb-files-group' : undefined">
        <div
          v-if="group.folder"
          class="mt-2 px-2.5 pb-[3px] pt-1.5 type-label truncate text-fg-tertiary"
          :title="group.folder"
        >
          {{ group.folder }}
        </div>
        <div
          v-for="file in group.files"
          :key="file.id"
          data-testid="manuscript-file"
          class="tsb-file"
          :class="{ 'is-active': file.path === activePath }"
        >
          <InlineRename
            v-if="naming === file.path"
            :title="file.path"
            :label="`Rename ${file.path}`"
            @commit="
              naming = null;
              $event !== file.path && emit('rename', file.path, $event);
            "
            @cancel="naming = null"
          /><template v-else
            ><button
              type="button"
              class="tsb-file-open"
              :aria-current="file.path === activePath ? 'true' : undefined"
              :title="file.path"
              @click="emit('open', file.path)"
            >
              <span class="tsb-file-glyph" aria-hidden="true"
                ><svg
                  width="22"
                  height="22"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="1.75"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                >
                  <template v-if="/\.(png|jpg|jpeg|gif|svg|webp)$/i.test(file.path)">
                    <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
                    <circle cx="9" cy="10" r="1.5" />
                    <path d="M4 17l4.5-4 3 2.5 3-3L20 17" />
                  </template>
                  <template v-else-if="/\.(csv|tsv|parquet)$/i.test(file.path)">
                    <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
                    <path d="M3.5 9.5h17M9 4.5v15" />
                  </template>
                  <template v-else-if="/\.ipynb$/i.test(file.path)">
                    <rect x="5" y="3.5" width="14" height="17" rx="2" />
                    <path d="M8 3.5v17M11 8h5M11 12h5" />
                  </template>
                  <path
                    v-else-if="/\.(py|ts|tsx|js|r|json|sh)$/i.test(file.path)"
                    d="M9 8l-4 4 4 4M15 8l4 4-4 4"
                  />
                  <path
                    v-else
                    :d="
                      'M13 3H6.5A1.5 1.5 0 0 0 5 4.5v15A1.5 1.5 0 0 0 6.5 21h11a1.5 1.5 0 0 0 1.5-1.5V9m-6-6 6 6m-6-6v6h6' +
                      (/\.(md|pdf|txt)$/i.test(file.path) ? 'M8.5 13h7M8.5 16.5h5' : '')
                    "
                  /></svg></span
              ><span class="tsb-file-name">{{ file.path.split('/').at(-1) }}</span>
            </button>
            <div v-if="file.path !== 'main.tex'" class="tsb-file-actions">
              <FileMenu
                :label="`More — ${file.path}`"
                @select="$event === 'Rename' ? (naming = file.path) : (removing = file.path)"
              /></div
          ></template>
        </div></div></template
    ><CoresearchModal v-if="removing !== null" label="Remove file" @close="removing = null"
      ><div
        class="w-full max-w-[460px] overflow-hidden rounded-xl border border-line bg-surface shadow-2xl"
      >
        <div class="flex items-center justify-between px-5 pb-3 pt-4">
          <h2 class="type-heading text-fg">Remove file?</h2>
          <button
            type="button"
            aria-label="Close"
            class="grid h-7 w-7 place-items-center rounded-md text-fg-subtle hover:bg-surface-2 hover:text-fg"
            @click="removing = null"
          >
            ×
          </button>
        </div>
        <div class="space-y-3 px-5 pb-1">
          <p class="type-small text-fg-subtle">
            Remove <strong>{{ removing }}</strong> from this paper? Any version saved before now
            still holds it; nothing else does.
          </p>
          <p v-if="error" role="alert" class="type-small text-danger">{{ error }}</p>
        </div>
        <div class="mt-4 flex items-center justify-end gap-2 border-t border-line px-5 py-3">
          <button
            type="button"
            class="inline-flex h-8 items-center gap-1.5 rounded-lg px-3 type-label text-fg-muted transition-colors hover:bg-surface-2 hover:text-fg"
            @click="removing = null"
          >
            Cancel</button
          ><button
            type="button"
            autofocus
            :disabled="busy"
            class="inline-flex h-8 items-center gap-1.5 rounded-lg bg-accent px-3 type-label text-white transition-opacity hover:opacity-90 bg-danger disabled:opacity-50"
            @click="confirmRemove"
          >
            Remove
          </button>
        </div>
      </div></CoresearchModal
    >
  </div>
</template>
