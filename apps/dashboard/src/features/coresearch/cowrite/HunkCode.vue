<script setup lang="ts">
// Ported from Lykeion-L2 packages/ui/src/components/tasks/HunkCode.tsx; see L2-LICENSE.
// One side of a proposed hunk, painted as the paper editor paints it, read-only.
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { EditorState } from '@codemirror/state';
import { EditorView } from '@codemirror/view';
import { StreamLanguage, syntaxHighlighting } from '@codemirror/language';
import { stex } from '@codemirror/legacy-modes/mode/stex';
import { latexHighlight } from './lib/latex-highlight';
const language = StreamLanguage.define(stex);
const props = defineProps<{ text: string; testId: string }>();
const host = ref<HTMLElement>();
let view: EditorView | undefined;
function draw() {
  view?.destroy();
  if (!host.value) return;
  view = new EditorView({
    parent: host.value,
    state: EditorState.create({
      doc: props.text,
      extensions: [
        EditorState.readOnly.of(true),
        EditorView.editable.of(false),
        language,
        syntaxHighlighting(latexHighlight),
        EditorView.lineWrapping,
      ],
    }),
  });
}
onMounted(draw);
watch(() => props.text, draw);
onBeforeUnmount(() => view?.destroy());
</script>
<template>
  <div ref="host" :data-testid="testId" />
</template>
