<script setup lang="ts">
import { ref, watch } from 'vue';
import type { highlightToolCode } from '../lib/tool-code';
import CopyButton from './CopyButton.vue';
const props = defineProps<{ code: string; lang?: string; live?: boolean; lineNumbers?: boolean }>();
const lines = ref<Awaited<ReturnType<typeof highlightToolCode>>>();
watch(
  () => [props.code, props.lang, props.live],
  async (_, __, onCleanup) => {
    let cancelled = false;
    onCleanup(() => (cancelled = true));
    lines.value = undefined;
    if (props.live || !props.lang) return;
    try {
      const { highlightToolCode } = await import('../lib/tool-code');
      const result = await highlightToolCode(
        props.code,
        ({ ts: 'typescript', js: 'javascript', sh: 'bash' } as Record<string, string>)[
          props.lang
        ] ?? props.lang,
      );
      if (!cancelled) lines.value = result;
    } catch {
      /* Unsupported content stays plain. */
    }
  },
  { immediate: true },
);
</script>
<template>
  <div class="code-block" :class="{ 'code-block--numbered': lineNumbers }">
    <div class="code-block-head">
      <slot name="meta"
        ><span v-if="lang" class="code-block-lang">{{ lang }}</span></slot
      ><CopyButton :text="code" code />
    </div>
    <div v-if="lines" class="code-block-pre" data-testid="code-block-pre">
      <pre
        style="background-color: #24292e; color: #e1e4e8"
      ><code><template v-for="(line,i) in lines" :key="i"><span class="line"><span v-for="(token,j) in line" :key="j" :style="{color:token.color}">{{ token.content }}</span></span>{{ i < lines.length - 1 ? '\n' : '' }}</template></code></pre>
    </div>
    <pre
      v-else
      class="code-block-pre"
      data-testid="code-block-pre"
    ><code v-if="lineNumbers"><span v-for="(line,i) in code.replace(/\n$/, '').split('\n')" :key="i" class="line">{{ line }}{{ '\n' }}</span></code><code v-else>{{ code }}</code></pre>
  </div>
</template>
