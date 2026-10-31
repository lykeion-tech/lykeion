import { describe, expect, it } from 'vitest';
import { toolStepTitle } from './tool-step-title';
import { activitySteps } from './run-transcript';
import type { RunMessage } from '../../api/types';

describe('readable action fallbacks', () => {
  it.each([
    ['Bash', { command: 'lyk record put --json "secret payload"' }, 'Save a research record'],
    ['Bash', { command: "env TOKEN=x zsh -lc 'lyk output put /secret/path'" }, 'Save a run output'],
    ['shellToolCall', { command: 'bash -c "lyk run list"' }, 'List research runs'],
    ['Bash', { command: 'cd /secret && rg "private query" .' }, 'Search files'],
    ['Bash', { command: 'python /private/science.py\ncat /secret' }, 'Run a Python script'],
    ['Bash', { command: 'some_unknown --payload {"x":1}' }, 'Run a shell command'],
    ['Read', { file_path: '/secret/file.txt' }, 'Read a file'],
    ['MultiEdit', { file_path: '/secret/file.txt' }, 'Edit a file'],
    ['Write', { file_path: '/secret/file.txt' }, 'Write a file'],
    ['Grep', { pattern: 'secret query' }, 'Search files'],
    ['mcp__web__search', { query: 'secret query' }, 'Search for information'],
    ['mcp__notebook__execute_python_cell', { code: 'secret code' }, 'Run a Python notebook cell'],
    [
      'mcp__lyk_door__call_tool',
      { tool: 'mcp__notebook__execute_r_cell', args: { code: 'secret' } },
      'Run an R notebook cell',
    ],
    ['strange_tool', { payload: 'secret' }, 'Use research tool'],
    ['', {}, 'Use research tool'],
    ['Bash', { command: { secret: 1 } }, 'Run a shell command'],
  ])('%s produces %s without payloads', (name, input, title) => {
    expect(toolStepTitle(name as string, input as Record<string, unknown>)).toBe(title);
  });

  it.each(['constructor', 'toString', '__proto__'])('ignores inherited lookup keys %s', (key) => {
    expect(toolStepTitle('Bash', { command: key })).toBe('Run a shell command');
  });

  it.each(['constructor', 'toString', '__proto__'])(
    'ignores inherited notebook languages %s',
    (key) => {
      expect(toolStepTitle('run_code', { language: key })).toBe('Run a notebook cell');
    },
  );

  it('ignores inherited notebook language in a tool name', () => {
    expect(toolStepTitle('execute_constructor_cell', {})).toBe('Run a notebook cell');
  });

  it('retains original records, full command/output and title precedence', () => {
    const command = 'unknown_command --json {"private":"payload"}\nsecond command';
    const message: RunMessage = {
      agent_run_id: 'r',
      attempt: 1,
      seq: 1,
      type: 'tool_use',
      tool: 'Bash',
      created_at: '',
      content: {
        type: 'tool_use',
        id: 'a',
        input: { command, description: 'Inspect research inputs' },
      },
    };
    const result: RunMessage = {
      ...message,
      seq: 2,
      type: 'tool_result',
      content: { tool_use_id: 'a', content: 'complete\noutput' },
    };
    const metadata: RunMessage = {
      ...message,
      seq: 3,
      type: 'status',
      content: {
        type: 'tool_step_titles',
        version: 1,
        titles: [{ seq: 1, title: 'Inspect recorded inputs' }],
      },
    };
    const step = activitySteps([message, result, metadata])[0]!;
    expect(step.title).toBe('Inspect recorded inputs');
    expect(step.command).toBe(command);
    expect(step.output).toBe('complete\noutput');
    expect(step.messages).toEqual([message, result]);
    expect(activitySteps([message])[0]!.title).toBe('Inspect research inputs');
    const bare = { ...message, content: { input: { command } } };
    expect(activitySteps([bare])[0]!.title).toBe('Run a shell command');
  });
});
