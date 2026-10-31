<script setup lang="ts">
// Ported from Lykeion-L2 packages/ui/src/components/tasks/MergeChangesRow.tsx; see L2-LICENSE.
// What a turn wrote to the paper: how many files it touched, how far each
// moved, and the way into the hunks one at a time. Always the proposal as it
// stands now, re-read as verdicts land.
import { computed, nextTick, ref, watch } from 'vue';
import CoresearchModal from '../CoresearchModal.vue';
import HunkCode from './HunkCode.vue';
import type { Change } from './types';
export interface ProposalHunk {
  id: string;
  status: Change['status'];
  oldText: string;
  newText: string;
}
export interface Proposal {
  id: string;
  files: { path: string; hunks: ProposalHunk[] }[];
}
const props = defineProps<{ proposal: Proposal }>();
const emit = defineEmits<{ settle: [string, 'keep' | 'undo'] }>();
// One word per state of a hunk, shared by the row's counts and the modal.
const HUNK_STATE: Record<Change['status'], { count: string; verdict: string }> = {
  pending: { count: 'pending', verdict: 'Pending' },
  kept: { count: 'kept', verdict: 'Kept' },
  undone: { count: 'undone', verdict: 'Undone' },
  stale: { count: 'superseded', verdict: 'Superseded' },
};
const STATUSES: Change['status'][] = ['pending', 'kept', 'undone', 'stale'];
const lines = (text: string) => (text === '' ? 0 : text.replace(/\n$/, '').split('\n').length);
function tally(file: Proposal['files'][number]) {
  let added = 0,
    removed = 0;
  const counts = new Map<Change['status'], number>();
  for (const hunk of file.hunks) {
    added += lines(hunk.newText);
    removed += lines(hunk.oldText);
    counts.set(hunk.status, (counts.get(hunk.status) ?? 0) + 1);
  }
  return {
    added,
    removed,
    status: STATUSES.flatMap((s) =>
      counts.get(s) ? [`${counts.get(s)} ${HUNK_STATE[s].count}`] : [],
    ).join(' · '),
  };
}
const open = ref(false),
  opener = ref<HTMLButtonElement>(),
  card = ref<HTMLElement>();
const hunks = computed(() => props.proposal.files.flatMap((f) => f.hunks));
const pending = computed(() => hunks.value.some((h) => h.status === 'pending'));
// Not "nothing pending": a stale hunk is the paper moving past it, not a
// decision anybody made.
const settledWord = computed(() =>
  hunks.value.length === 0
    ? 'Applied'
    : hunks.value.some((h) => h.status === 'kept' || h.status === 'undone')
      ? 'Accepted'
      : 'Superseded',
);
// Review is for what is still owed: the modal goes once nothing is pending.
watch(pending, (owed) => {
  if (!owed) open.value = false;
});
// Back to the button that raised the modal, or to the card when settling took
// that button away.
watch(open, async (now, was) => {
  if (now || !was) return;
  await nextTick();
  (opener.value ?? card.value)?.focus();
});
</script>
<template>
  <div ref="card" class="merge-changes" data-testid="merge-changes" tabindex="-1">
    <div class="merge-changes-head">
      <span class="merge-changes-title"
        >{{ proposal.files.length }}
        {{ proposal.files.length === 1 ? 'file' : 'files' }} edited</span
      ><button
        v-if="pending"
        ref="opener"
        type="button"
        class="merge-changes-review"
        @click="open = true"
      >
        Review</button
      ><span v-else class="merge-changes-accepted">{{ settledWord }}</span>
    </div>
    <ul class="merge-changes-files">
      <li
        v-for="file in proposal.files"
        :key="file.path"
        class="merge-changes-file"
        :data-testid="`merge-changes-file-${file.path}`"
      >
        <span class="merge-changes-path">{{ file.path }}</span
        ><template v-if="file.hunks.length"
          ><span class="merge-changes-added">+{{ tally(file).added }}</span
          ><span class="merge-changes-removed">−{{ tally(file).removed }}</span
          ><span class="merge-changes-status">{{ tally(file).status }}</span></template
        >
      </li>
    </ul>
    <CoresearchModal v-if="open" label="Review changes" @close="open = false">
      <div class="merge-modal">
        <div class="merge-modal-head">
          <h2 class="merge-modal-heading">Review changes</h2>
          <button type="button" class="merge-modal-close" aria-label="Close" @click="open = false">
            <svg
              width="15"
              height="15"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.75"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M6 6l12 12M18 6 6 18" />
            </svg>
          </button>
        </div>
        <div class="merge-modal-body">
          <section v-for="file in proposal.files" :key="file.path" class="merge-modal-file">
            <h3 class="merge-modal-path">{{ file.path }}</h3>
            <div
              v-for="hunk in file.hunks"
              :key="hunk.id"
              class="merge-hunk"
              :data-testid="`merge-hunk-${hunk.id}`"
            >
              <HunkCode
                v-if="hunk.oldText"
                class="merge-hunk-old"
                :text="hunk.oldText"
                test-id="merge-hunk-old"
              /><HunkCode
                v-if="hunk.newText"
                class="merge-hunk-new"
                :text="hunk.newText"
                test-id="merge-hunk-new"
              />
              <div v-if="hunk.status === 'pending'" class="merge-hunk-verdicts">
                <button
                  type="button"
                  class="merge-hunk-undo"
                  @click="emit('settle', hunk.id, 'undo')"
                >
                  Undo</button
                ><button
                  type="button"
                  class="merge-hunk-keep"
                  @click="emit('settle', hunk.id, 'keep')"
                >
                  Keep
                </button>
              </div>
              <span v-else class="merge-hunk-settled">{{ HUNK_STATE[hunk.status].verdict }}</span>
            </div>
          </section>
        </div>
      </div>
    </CoresearchModal>
  </div>
</template>
