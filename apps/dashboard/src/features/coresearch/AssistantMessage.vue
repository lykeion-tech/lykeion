<script lang="ts">
import { defineComponent, h, type VNode } from 'vue';
import { parseMarkdownBlocks } from '../../lib/markdown';
import CodeBlock from './CodeBlock.vue';

export default defineComponent({
  props: {
    text: { type: String, required: true },
    prose: Boolean,
    live: Boolean,
    redactLocalPaths: Boolean,
  },
  setup(props) {
    return () => {
      const tokens = parseMarkdownBlocks(props.text, props.redactLocalPaths);
      let index = 0;
      function children(): VNode[] {
        const nodes: VNode[] = [];
        while (index < tokens.length) {
          const token = tokens[index++]!;
          if (token.nesting === -1) break;
          if (token.type === 'fence' || token.type === 'code_block') {
            nodes.push(
              h(CodeBlock, {
                code: token.content.replace(/\n$/, ''),
                lang: token.type === 'fence' ? token.info.trim().split(/\s+/)[0] : undefined,
                live: props.live,
              }),
            );
          } else if (token.type === 'inline') {
            // Only tight-list paragraphs need this neutral inline wrapper.
            nodes.push(h('span', { innerHTML: token.html }));
          } else {
            const attrs = Object.fromEntries(token.attrs ?? []);
            const inline = tokens[index];
            let node: VNode;
            if (
              token.nesting === 1 &&
              inline?.type === 'inline' &&
              tokens[index + 1]?.nesting === -1
            ) {
              // A whole inline fragment is balanced within its block. Never
              // split rendered HTML across component or container boundaries.
              index += 2;
              node = h(token.hidden ? 'span' : token.tag, { ...attrs, innerHTML: inline.html });
            } else {
              node = h(token.tag, attrs, token.nesting === 1 ? children() : undefined);
            }
            if (token.type === 'table_open') {
              node = h(
                'div',
                { class: 'markdown-table', tabindex: 0, role: 'region', 'aria-label': 'Table' },
                [node],
              );
            }
            nodes.push(node);
          }
        }
        return nodes;
      }
      return h('div', { class: props.prose ? 'md-prose' : 'msg msg--assistant' }, children());
    };
  },
});
</script>
<style scoped>
:deep(.markdown-table) {
  overflow-x: auto;
  max-width: 100%;
}
</style>
