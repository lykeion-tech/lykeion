import { expect, it } from 'vitest';
import { h } from 'vue';
import { all, createTreeApp, node, textOf } from '../../../../../tests/daemon-tree';
import AssistantMessage from './AssistantMessage.vue';
it('hides local machine paths only in agent prose while retaining context, web links and code', () => {
  const app = createTreeApp({ render: () => h(AssistantMessage, { redactLocalPaths: true, text: 'Failed to save /private/tmp/lyk-123/output.csv. Retry [report](/Users/alice/project/out.csv). Visit https://example.com/a/b.\n\n`/tmp/example.py`\n\n```sh\ncat /tmp/a\n```' }) });
  const root = node('root'); app.mount(root);
  const html = all(root).map(n => String(n.props.innerHTML ?? '')).join('');
  expect(html).toContain('Failed to save'); expect(html).toContain('[local file]');
  expect(html).not.toContain('/private/tmp/lyk-123'); expect(html).not.toContain('/Users/alice/project');
  expect(html).toContain('href="https://example.com/a/b"'); expect(html).not.toContain('/tmp/example.py');
  expect(textOf(root)).toContain('cat /tmp/a'); app.unmount();
});
it('preserves paths in content without the agent-prose flag', () => {
 const app = createTreeApp({ render: () => h(AssistantMessage, { text: 'My path is /tmp/report.csv' }) });
 const root = node('root'); app.mount(root); expect(all(root).map(n => String(n.props.innerHTML ?? '')).join('')).toContain('/tmp/report.csv'); app.unmount();
});
it('cleans absolute paths in inline path labels, copied prose, and spaced filenames without changing web links or fenced code', async () => {
 const { cleanAssistantText } = await import('./assistant-paths');
 const source = 'Failed to save `/Users/alice/Lab Stuff/report.csv`. See [report](/data/project/my result.csv). Visit https://example.com/data/x.\n\n```sh\ncat /tmp/a\n```';
 const cleaned = cleanAssistantText(source);
 expect(cleaned).not.toContain('/Users/alice'); expect(cleaned).not.toContain('Lab Stuff');
 expect(cleaned).not.toContain('/data/project'); expect(cleaned).not.toContain('my result.csv');
 expect(cleaned).toContain('Failed to save'); expect(cleaned).toContain('https://example.com/data/x');
 expect(cleaned).toContain('cat /tmp/a');
});
it('cleans common path roots and local link labels while keeping browser URLs', async () => {
 const { cleanAssistantText } = await import('./assistant-paths');
 const input = 'Failed: [file:///data/run/result.csv](file:///data/run/result.csv), /srv/lab/a.txt, /Volumes/Lab/file.csv and C:\\Users\\me\\out.csv. See https://example.com/data/run/result.csv';
 const output = cleanAssistantText(input);
 for (const local of ['file:///data/', '/srv/', '/Volumes/', 'C:\\Users\\me']) expect(output).not.toContain(local);
 expect(output).toContain('https://example.com/data/run/result.csv'); expect(output).toContain('Failed:');
});
it('preserves the cause of a failure when a later word resembles a filename', async () => {
 const { cleanAssistantText } = await import('./assistant-paths');
 expect(cleanAssistantText('Could not save /tmp/a because report.csv was locked')).toBe('Could not save [local file] because report.csv was locked');
});
