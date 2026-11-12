import { defineAsyncComponent, defineComponent, h } from 'vue';

// Recorded text stays readable as plain text while the Markdown module loads and
// when it fails to load. onFail runs once the load has failed.
export function lazyRecordedMarkdown(onFail?: () => void) {
  const plain = defineComponent({
    props: { content: { type: String, default: '' }, localFiles: Boolean, breaks: Boolean },
    setup: (text) => () =>
      h(
        'div',
        {
          class: 'recorded-text-fallback',
          style: { whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' },
        },
        text.content,
      ),
  });
  return defineAsyncComponent({
    loader: () => import('./RecordedMarkdown.vue'),
    loadingComponent: plain,
    errorComponent: plain,
    delay: 0,
    onError: (_error, _retry, fail) => {
      onFail?.();
      fail();
    },
  });
}
