import { afterEach, describe, expect, it } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from '@vue/server-renderer';
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import RunTurn from '../apps/dashboard/src/components/RunTurn.vue';
import { workspaceId } from '../apps/dashboard/src/api/session';
import { keys } from '../apps/dashboard/src/api/keys';
import type { AgentRun, RunMessage } from '../apps/dashboard/src/api/types';

const run = {
  id: 'r',
  workspace_id: 'w',
  card_id: 'c',
  responsible_user_id: 'u',
  board_column: 'grounding',
  round: 1,
  role: 'scoper',
  harness: 'claude',
  status: 'completed',
  queue_reason: null,
  runtime_id: null,
  dispatched_at: null,
  started_at: null,
  completed_at: null,
  attempt: 2,
  max_attempts: 3,
  model: '',
  error: '',
  failure_reason: null,
  cost_usd: 0,
  created_at: '2026-10-15T10:00:00Z',
  handoff_note: '',
} satisfies AgentRun;
function msg(seq: number, type: RunMessage['type'], content: unknown, tool = ''): RunMessage {
  return { agent_run_id: 'r', attempt: 2, seq, type, content, tool, created_at: run.created_at };
}
async function render(messages: RunMessage[], overrides: Partial<AgentRun> = {}) {
  workspaceId.value = 'w';
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  });
  client.setQueryData(keys.runMessages('w', 'r', 2), messages);
  const app = createSSRApp({
    render: () => h(RunTurn, { run: { ...run, ...overrides }, machine: 'Recorded machine' }),
  });
  app.use(VueQueryPlugin, { queryClient: client });
  try {
    return await renderToString(app);
  } finally {
    client.clear();
  }
}
afterEach(() => {
  workspaceId.value = '';
});
describe('reading-first recorded run', () => {
  it('renders all writing in sequence around compact activity and never repeats the final text', async () => {
    const html = await render([
      msg(1, 'text', '# Evidence\n\nFirst **recorded** paragraph.'),
      msg(2, 'tool_use', { command: 'lyk card show' }, 'Bash'),
      msg(3, 'thinking', 'Private recorded reasoning'),
      msg(4, 'tool_result', { tool_use_id: 'dropped-id', content: 'refused', is_error: true }),
      msg(5, 'text', '## Outcome\n\nLast paragraph.'),
    ]);
    expect(html).toContain('<h1>Evidence</h1>');
    expect(html).toContain('<strong>recorded</strong>');
    expect(html.indexOf('First <strong>')).toBeLessThan(html.indexOf('Activity'));
    expect(html.indexOf('Activity')).toBeLessThan(html.indexOf('<h2>Outcome</h2>'));
    expect(html.match(/Last paragraph\./g)).toHaveLength(1);
    expect(html).toContain('data-activity-group');
    expect(html).toContain('lyk card show');
    expect(html).toContain('Recorded reasoning');
    expect(html).not.toMatch(/<details[^>]*\bopen(?:=|>)/);
    expect(html).toContain('Error');
    expect(html).toContain('Attempt 2');
    expect(html).toContain('Sequence 4');
  });
  it('coalesces adjacent text fragments without crossing activity boundaries', async () => {
    const html = await render([
      msg(1, 'text', '## Stream'),
      msg(2, 'text', 'ed\n\nBefore'),
      msg(3, 'status', 'waiting'),
      msg(4, 'text', 'After'),
    ]);
    expect(html).toContain('<h2>Streamed</h2>');
    expect(html).toContain('<p>Before</p>');
    expect(html).toContain('<p>After</p>');
  });
  it('keeps Codex completed command errors and output in their recorded tool entry', async () => {
    const html = await render(
      [
        msg(
          1,
          'tool_use',
          {
            type: 'item.completed',
            item: {
              id: 'c1',
              type: 'command_execution',
              command: 'python check.py',
              aggregated_output: 'actual stderr',
              exit_code: 1,
              status: 'failed',
            },
          },
          'shell',
        ),
      ],
      { harness: 'codex' },
    );
    expect(html).toContain('python check.py');
    expect(html).toContain('actual stderr');
    expect(html).toContain('Error');
    expect(html).not.toContain('Succeeded');
  });
  it('preserves unknown tools and non-string writing as safe inspectable recorded data', async () => {
    const html = await render([
      msg(1, 'tool_use', { strange: ['payload'] }, 'unknown-x'),
      msg(2, 'text', { plain: 'structured text' }),
      msg(3, 'tool_use', { path: 'data.xlsx' }, 'spreadsheet_custom'),
    ]);
    expect(html).toContain('unknown-x');
    expect(html).toMatch(/class="step-label"[^>]*>spreadsheet_custom</);
    expect(html).toContain('structured text');
    expect(html).toContain('payload');
    expect(html).toContain('Full recorded message');
  });
  it('renders tables, lists and unfinished code fences while keeping code escaped and copyable', async () => {
    const html = await render([
      msg(1, 'text', '- one\n- two\n\n|A|B|\n|-|-|\n|a|b|\n\n```js\n<script>alert(1)</script>'),
    ]);
    expect(html).toContain('<ul>');
    expect(html).toContain('<table>');
    expect(html).toContain('Copy code');
    expect(html).toContain('&lt;script&gt;alert(1)&lt;/script&gt;');
    expect(html).not.toContain('<script>');
  });
  it('blocks active HTML, Vue tags and unsafe URLs, and never loads images or diagram assets', async () => {
    const html = await render([
      msg(
        1,
        'text',
        '<script>alert(1)</script>\n<MyComponent @click="steal" />\n\n[x](javascript:alert(1)) [y](data:text/html,evil) [z](file:///tmp/a) [safe](https://example.com)\n\n![remote](https://example.com/tracker.png)\n\n```mermaid\ngraph TD; A-->B\n```',
      ),
    ]);
    expect(html).not.toMatch(/<(?:script|img|iframe|MyComponent)\b/i);
    expect(html).not.toMatch(/href="(?:javascript|data|file):/i);
    expect(html).toContain('href="https://example.com"');
    expect(html).toContain('rel="noopener noreferrer"');
    expect(html).toContain('remote');
    expect(html).toContain('graph TD; A--&gt;B');
  });
  it('keeps literal numeric citations and incomplete links honest through a later stream snapshot', async () => {
    const first = await render([msg(1, 'text', 'See [1] and [unfinished](https://exam')], {
      status: 'running',
    });
    expect(first).toContain('[1]');
    expect(first).toContain('unfinished');
    const later = await render([msg(1, 'text', 'See [1] and [finished](https://example.com).')], {
      status: 'completed',
    });
    expect(later).toContain('href="https://example.com"');
    expect(later).not.toContain('unfinished');
    expect(later).not.toContain('data-source');
  });
});
