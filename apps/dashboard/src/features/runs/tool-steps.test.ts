import { describe, expect, it } from 'vitest';
import { transcriptBlocks, activitySteps, isMemberResult } from './run-transcript';
import { memberResult } from './study-hand-in';
import type { RunMessage } from '../../api/types';
const msg = (
  seq: number,
  type: RunMessage['type'],
  content: unknown,
  tool = '',
  attempt = 1,
): RunMessage => ({ agent_run_id: 'r', attempt, seq, type, content, tool, created_at: '' });
const call = (seq: number, id: string, input: unknown = { command: 'python check.py' }) =>
  msg(seq, 'tool_use', { type: 'tool_use', id, name: 'Bash', input }, 'Bash');
describe('member hand-in tool output', () => {
  it('finds the member tool name inside a Codex event', () => {
    expect(
      isMemberResult(
        msg(1, 'tool_use', {
          item: { type: 'mcp_tool_call', server: 'lyk_door', tool: 'member_result' },
        }),
      ),
    ).toBe(true);
    expect(
      isMemberResult(
        msg(2, 'tool_use', {
          item: { type: 'mcp_tool_call', server: 'other', tool: 'member_result' },
        }),
      ),
    ).toBe(false);
  });
  it('recognizes a completed member_result envelope and preserves trailing comments', () => {
    const result = memberResult(
      'mcp__lyk_door__member_result',
      '{"status":"done","hand_in":{"agent_run_id":"r1","attempt":1,"hand_in":{"answer":"Two sources.","papers":[]}}}\n\nResearcher comment',
    );
    expect(result?.handIn.answer).toBe('Two sources.');
    expect(result?.agentRunId).toBe('r1');
    expect(result?.trailing).toBe('Researcher comment');
  });
  it('leaves unrelated, working and malformed tool outputs alone', () => {
    expect(memberResult('other', '{"status":"done","hand_in":{}}')).toBeNull();
    expect(memberResult('member_result', '{"status":"working","hand_in":null}')).toBeNull();
    expect(memberResult('member_result', '{broken')).toBeNull();
  });
});
describe('logical tool steps', () => {
  it('exposes recorded notebook code as a tool step without treating file writes as cells', () => {
    const source = 'print("transcript UI check")';
    const steps = activitySteps([
      msg(
        1,
        'tool_use',
        {
          id: 'cell',
          input: { code: source, human_description: 'Print transcript check', execution_count: 3 },
        },
        'mcp__notebook__execute_python_cell',
      ),
      msg(2, 'tool_result', { tool_use_id: 'cell', content: 'transcript UI check' }),
      msg(
        3,
        'tool_use',
        { id: 'write', input: { file_path: 'example.py', content: source } },
        'Write',
      ),
    ]);
    expect(steps[0]!.notebook).toEqual({ code: source, language: 'python', executionCount: 3 });
    expect(steps[0]!.output).toBe('transcript UI check');
    expect(steps[1]!.notebook).toBeUndefined();
  });
  it('unwraps addressed notebook calls while preserving their original records', () => {
    const call = msg(
      1,
      'tool_use',
      {
        id: 'cell',
        input: {
          tool: 'mcp__notebook__execute_r_cell',
          args: { code: 'print(1)', human_description: 'Print the test value' },
        },
      },
      'mcp__lyk_door__call_tool',
    );
    const step = activitySteps([call])[0]!;
    expect(step.notebook).toEqual({ code: 'print(1)', language: 'r', executionCount: undefined });
    expect(step.title).toBe('Print the test value');
    expect(step.messages).toEqual([call]);
  });
  it('shows shell notebook commands in direct and addressed tool calls', () => {
    for (const addressed of [false, true]) {
      const args = { command: 'printf "second step\\n"' };
      const input = addressed ? { tool: 'mcp__notebook__execute_shell_cell', args } : args;
      const tool = addressed ? 'mcp__lyk_door__call_tool' : 'mcp__notebook__execute_shell_cell';
      expect(
        activitySteps([msg(1, 'tool_use', { id: 'shell', input }, tool)])[0]!.notebook,
      ).toEqual({ code: args.command, language: 'shell', executionCount: undefined });
    }
  });
  it('requires a notebook or Lykeion execution address before displaying a cell', () => {
    for (const tool of ['notexecute_python_cell', 'mcp__other__execute_python_cell', 'Write']) {
      expect(
        activitySteps([
          msg(
            1,
            'tool_use',
            { input: { code: 'print(1)', name: 'mcp__notebook__execute_python_cell' } },
            tool,
          ),
        ])[0]!.notebook,
      ).toBeUndefined();
    }
    expect(
      activitySteps([
        msg(
          1,
          'tool_use',
          {
            input: {
              tool: 'mcp__notebook__execute_python_cell',
              server: 'other',
              args: { code: 'print(1)' },
            },
          },
          'mcp__lyk_door__call_tool',
        ),
      ])[0]!.notebook,
    ).toBeUndefined();
    expect(
      activitySteps([
        msg(
          1,
          'tool_use',
          { input: { code: 'print(1)', language: 'python' } },
          'mcp__lyk_door__run_code',
        ),
      ])[0]!.notebook?.code,
    ).toBe('print(1)');
  });
  it('uses Codex server provenance without changing the recorded tool label', () => {
    const cell = msg(1, 'tool_use', {
      item: {
        id: 'c',
        type: 'mcp_tool_call',
        server: 'notebook',
        tool: 'execute_python_cell',
        arguments: { code: 'print(42)' },
        status: 'completed',
      },
    });
    expect(activitySteps([cell])[0]!.notebook?.code).toBe('print(42)');
    expect(cell.tool).toBe('');
  });
  it('reads the door server plus bare tool envelope used by real MCP calls', () => {
    for (const language of ['python', 'shell']) {
      const input = {
        server: 'notebook',
        tool: `execute_${language}_cell`,
        args: { code: 'print(1)' },
        human_description: 'Print the test value',
      };
      const step = activitySteps([
        msg(1, 'tool_use', { id: 'cell', input }, 'mcp__lyk_door__call_tool'),
      ])[0]!;
      expect(step.notebook?.code).toBe('print(1)');
      expect(step.notebook?.language).toBe(language);
      expect(step.title).toBe('Print the test value');
    }
  });
  it('pairs out-of-order results by identity and preserves complete records', () => {
    const steps = activitySteps([
      call(1, 'a'),
      call(2, 'b'),
      msg(3, 'tool_result', { tool_use_id: 'b', content: 'B' }),
      msg(4, 'tool_result', { tool_use_id: 'a', content: 'A', is_error: true }),
    ]);
    expect(steps).toHaveLength(2);
    expect(steps[0]!.messages.map((m) => m.seq)).toEqual([1, 4]);
    expect(steps[0]!.output).toBe('A');
    expect(steps[0]!.error).toBe(true);
    expect(steps[1]!.output).toBe('B');
  });
  it('never attaches unmatched or cross-attempt results to a call', () => {
    const steps = activitySteps([
      call(1, 'a'),
      msg(2, 'tool_result', { tool_use_id: 'missing', content: 'unmatched' }),
      msg(3, 'tool_result', { tool_use_id: 'a', content: 'other attempt' }, '', 2),
    ]);
    expect(steps).toHaveLength(3);
    expect(steps[0]!.output).toBe('');
  });
  it('prefers persisted names over supplied descriptions and does not draw metadata rows', () => {
    const messages = [
      call(1, 'a', { command: 'python check.py', human_description: 'Check the fitted model' }),
      msg(2, 'status', {
        type: 'tool_step_titles',
        version: 1,
        titles: [{ seq: 1, title: 'Check model residuals' }],
      }),
    ];
    const blocks = transcriptBlocks(messages);
    expect(blocks).toHaveLength(1);
    expect(activitySteps(blocks[0]!.messages)[0]!.title).toBe('Check model residuals');
    expect(activitySteps(blocks[0]!.messages)).toHaveLength(1);
  });
  it('keeps supplied descriptions while invalid or unrelated names are ignored', () => {
    const steps = activitySteps([
      call(1, 'a', { human_description: 'Fit the survival model' }),
      msg(2, 'status', {
        type: 'tool_step_titles',
        version: 1,
        titles: [
          { seq: 9, title: 'Other call' },
          { seq: 1, title: '<script>bad</script>' },
        ],
      }),
    ]);
    expect(steps[0]!.title).toBe('Fit the survival model');
    expect(
      activitySteps([call(1, 'a', { command: 'ls', human_description: 'bad\nlabel' })])[0]!.title,
    ).toBe('List files');
  });
  it('reads Codex MCP arguments and joins Cursor and Antigravity results', () => {
    const codex = msg(
      1,
      'tool_use',
      {
        item: {
          id: 'c',
          type: 'mcp_tool_call',
          server: 'lyk_door',
          tool: 'call_tool',
          arguments: {
            server: 'sources',
            tool: 'fetch_text',
            args: { url: 'https://example.org' },
            human_description: 'Read the full paper',
          },
          status: 'completed',
          result: { content: [{ text: 'paper' }] },
        },
      },
      'call_tool',
    );
    expect(activitySteps([codex])[0]!.title).toBe('Read the full paper');
    const cursor = [
      msg(
        1,
        'tool_use',
        { call_id: 'x', tool_call: { shellToolCall: { args: { command: 'ls' } } } },
        'shellToolCall',
      ),
      msg(
        2,
        'tool_result',
        {
          call_id: 'x',
          tool_call: { shellToolCall: { result: { success: { output: 'files' } } } },
        },
        'shellToolCall',
      ),
    ];
    expect(activitySteps(cursor)).toHaveLength(1);
    expect(activitySteps(cursor)[0]!.output).toBe('files');
    const agy = [
      msg(1, 'tool_use', {
        step_update: {
          step_index: 1,
          tool_name: 'run_command',
          tool_info: { parameters: { CommandLine: 'ls' } },
        },
      }),
      msg(2, 'tool_result', {
        step_update: { step_index: 1, state: 'DONE', tool_info: { output: 'files' } },
      }),
    ];
    expect(activitySteps(agy)).toHaveLength(1);
    expect(activitySteps(agy)[0]!.output).toBe('files');
  });
});
it('pairs across prose boundaries without moving or duplicating assistant messages', () => {
  const blocks = transcriptBlocks([
    call(1, 'a'),
    msg(2, 'text', 'The computation is running.'),
    msg(3, 'tool_result', { tool_use_id: 'a', content: '42' }),
  ]);
  expect(blocks.map((b) => b.kind)).toEqual(['activity', 'text']);
  expect(activitySteps(blocks[0]!.messages)[0]!.output).toBe('42');
  expect(activitySteps(blocks[0]!.messages)[0]!.running).toBe(false);
  expect(blocks[1]!.text).toBe('The computation is running.');
});
