<script setup lang="ts">
import { computed, ref } from 'vue';
import type { Comment } from './types';
import { renderMarkdown } from '../../../lib/markdown';
import { formatAgo } from './lib/pane-time';
import PaneViewPicker from './PaneViewPicker.vue';
import PaneIcon from './PaneIcon.vue';
const props = defineProps<{
  comments: (Comment & { path: string })[];
  people: { id: string; name: string }[];
  meId?: string;
}>();
const emit = defineEmits<{ delete: [id: string]; open: [comment: Comment & { path: string }] }>();
const view = ref('all'),
  query = ref('');
const views = [
  { id: 'all', label: 'All Comments' },
  { id: 'unresolved', label: 'Only Unresolved' },
  { id: 'resolved', label: 'Only Resolved' },
  { id: 'mine', label: 'Your Comments and Mentions' },
];
const author = (id: string) => props.people.find((p) => p.id === id)?.name ?? 'A former member';
const shown = computed(() =>
  [...props.comments]
    .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))
    .filter((c) =>
      view.value === 'unresolved'
        ? !c.resolved_at
        : view.value === 'resolved'
          ? !!c.resolved_at
          : view.value === 'mine'
            ? c.author_user_id === props.meId || (!!props.meId && c.mentions.includes(props.meId))
            : true,
    )
    .filter(
      (c) =>
        !query.value.trim() ||
        [c.body, c.quote, author(c.author_user_id)].some((s) =>
          s.toLowerCase().includes(query.value.trim().toLowerCase()),
        ),
    ),
);
</script>
<template>
  <div class="ms-pane" data-testid="comments-pane">
    <header class="art-header">
      <span class="art-title"><PaneIcon name="Chat" />Comments</span
      ><PaneViewPicker label="Comments view" :views="views" :value="view" @change="view = $event" />
    </header>
    <div class="ms-comments-body">
      <label class="ms-comments-search"
        ><PaneIcon name="Search" /><input
          v-model="query"
          type="search"
          placeholder="Search"
          aria-label="Search comments"
      /></label>
      <p v-if="!shown.length" class="ms-pane-empty">
        {{
          !comments.length
            ? 'No comments yet — select text in the paper and press Comment.'
            : 'No comments match.'
        }}
      </p>
      <ul v-else class="ms-comments-list">
        <li
          v-for="c in shown"
          :key="c.id"
          class="ms-comment"
          :class="{ 'is-resolved': !!c.resolved_at }"
          data-testid="manuscript-comment"
        >
          <div class="ms-comment-head">
            <span class="ms-comment-author">{{ author(c.author_user_id) }}</span
            ><span class="ms-comment-ago">{{ formatAgo(Date.parse(c.created_at) / 1000) }}</span
            ><span v-if="c.resolved_at" class="ms-comment-state">Resolved</span
            ><span class="ms-comment-acts"
              ><button
                v-if="c.author_user_id === meId"
                type="button"
                class="ms-comment-act"
                aria-label="Delete comment"
                title="Delete"
                @click="emit('delete', c.id)"
              >
                <PaneIcon name="Trash" width="14" height="14" /></button
            ></span>
          </div>
          <div class="ms-comment-text md-prose" v-html="renderMarkdown(c.body).html" />
          <button type="button" class="ms-comment-where" @click="emit('open', c)">
            on “{{ c.quote.length > 60 ? `${c.quote.slice(0, 60)}…` : c.quote }}” · {{ c.path }}:{{
              c.line
            }}
          </button>
        </li>
      </ul>
    </div>
  </div>
</template>
