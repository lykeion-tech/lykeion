import type { AttemptEnd, RunMessage } from '../../api/types';
import { toolStepTitle } from './tool-step-title';

export interface TranscriptBlock {
  key: string;
  kind: 'text' | 'activity';
  text: string;
  messages: RunMessage[];
}

// Text records are complete adapter messages.
export function transcriptBlocks(
  messages: readonly RunMessage[],
  { joinText = false } = {},
): TranscriptBlock[] {
  const blocks: TranscriptBlock[] = [];
  const owners = new Map<string, RunMessage>();
  const results = new Map<RunMessage, RunMessage[]>();
  const paired = new Set<RunMessage>();
  for (const message of sorted(messages)) {
    if (message.type !== 'tool_use' && message.type !== 'tool_result') continue;
    const call = toolCall(message);
    if (!call.id) continue;
    const key = `${message.attempt}:${call.id}`;
    if (message.type === 'tool_use') owners.set(key, message);
    else {
      const owner = owners.get(key);
      if (owner) {
        results.set(owner, [...(results.get(owner) ?? []), message]);
        paired.add(message);
      }
    }
  }
  for (const message of sorted(messages)) {
    if (paired.has(message)) continue;
    if (isStepTitles(message)) continue;
    const kind =
      message.type === 'text' && typeof message.content === 'string' ? 'text' : 'activity';
    const previous = blocks.at(-1);
    const text = kind === 'text' ? (message.content as string) : '';
    const sameAttempt = previous?.messages[0]?.attempt === message.attempt;
    if (kind === 'activity' && previous?.kind === kind && sameAttempt) {
      previous.messages.push(message, ...(results.get(message) ?? []));
    } else if (kind === 'text' && joinText && previous?.kind === kind && sameAttempt) {
      previous.text += text;
      previous.messages.push(message);
    } else {
      blocks.push({
        key: `${message.attempt}:${message.seq}`,
        kind,
        text,
        messages: [message, ...(results.get(message) ?? [])],
      });
    }
  }
  const metadata = messages.filter(isStepTitles);
  for (const block of blocks) {
    if (block.kind === 'activity')
      block.messages.push(...metadata.filter((m) => m.attempt === block.messages[0]?.attempt));
  }
  return blocks;
}

// Keep tool-title metadata for real calls, but never render internal events as steps.
export function visibleTranscriptMessages(messages: readonly RunMessage[]): RunMessage[] {
  return messages.filter((message) => {
    if (message.type === 'log' || (message.type === 'status' && !isStepTitles(message)))
      return false;
    if (message.type === 'text' || message.type === 'thinking')
      return typeof message.content === 'string'
        ? message.content.trim().length > 0
        : message.content != null;
    return true;
  });
}

function sorted(messages: readonly RunMessage[]) {
  return [...messages].sort((a, b) => a.attempt - b.attempt || a.seq - b.seq);
}
function object(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}
function filled(value: unknown): value is Record<string, unknown> {
  return Object.keys(object(value)).length > 0;
}
function string(...values: unknown[]): string {
  return (
    values.find((value): value is string => typeof value === 'string' && value.length > 0) ?? ''
  );
}
export function recordedContent(value: unknown): string {
  return typeof value === 'string' ? value : (JSON.stringify(value, null, 2) ?? String(value));
}
function readable(value: unknown): string {
  if (typeof value === 'string') return value;
  if (Array.isArray(value)) return value.map(readable).filter(Boolean).join('\n');
  const data = object(value);
  return string(
    data.text,
    data.message,
    data.output,
    data.aggregated_output,
    data.stdout,
    data.stderr,
    data.errorMessage,
    typeof data.content === 'string' ? data.content : undefined,
  );
}
// Arguments some adapters record as JSON text.
function args(value: unknown): Record<string, unknown> {
  if (typeof value !== 'string') return object(value);
  try {
    return object(JSON.parse(value));
  } catch {
    return {};
  }
}

interface ToolCall {
  server?: string;
  // Claude pairs tool calls with their tool_use id. Codex records a call complete, with
  // its own output, under its item id.
  id: string;
  tool: string;
  input: Record<string, unknown>;
  output: string;
  state: string;
  exit: number | undefined;
  error: boolean;
}
// One recorded tool message in the shape each daemon adapter stores it
// (server/pkg/agent): Codex's item.completed event or a Claude content block.
function toolCall(message: RunMessage): ToolCall {
  const content = object(message.content);
  const call: ToolCall = {
    id: '',
    tool: message.tool,
    input: {},
    output: '',
    state: '',
    exit: undefined,
    error: false,
  };
  if (filled(content.item)) {
    const item = object(content.item);
    const result = object(item.result);
    call.id = string(item.id);
    call.tool ||= string(item.tool, item.server);
    call.server = string(item.server);
    call.input =
      item.type === 'command_execution' ? { command: item.command } : args(item.arguments);
    call.output =
      readable(item.aggregated_output) ||
      readable(result.content) ||
      readable(item.output) ||
      readable(item.error);
    call.state = string(item.status);
    call.exit = typeof item.exit_code === 'number' ? item.exit_code : undefined;
    call.error = Boolean(item.error);
  } else if (filled(content.tool_call)) {
    const [name, value] = Object.entries(object(content.tool_call))[0]!;
    const body = object(value);
    const result = object(body.result);
    const success = object(result.success);
    call.id = string(content.call_id);
    call.tool ||= name;
    call.input = object(body.args);
    // An MCP call names the server's tool and nests its arguments.
    if (/^mcpToolCall$/.test(call.tool) && string(call.input.name)) {
      call.tool = string(call.input.name);
      call.input = args(call.input.args);
    }
    call.output = filled(success)
      ? readable(success) || readable(success.content) || recordedContent(success)
      : readable(Object.values(result)[0]);
    call.exit = typeof success.exitCode === 'number' ? success.exitCode : undefined;
    call.error = filled(result) && !filled(success);
  } else if (filled(content.step_update)) {
    const step = object(content.step_update);
    const info = object(step.tool_info);
    call.id = typeof step.step_index === 'number' ? `step:${step.step_index}` : '';
    call.tool ||= string(step.tool_name, info.name);
    call.input = object(info.parameters);
    call.output = readable(info.output) || readable(info.error);
    // The call is recorded at its first snapshot; only its result is final.
    call.state = message.type === 'tool_result' ? string(step.state).toLowerCase() : '';
    call.error = Boolean(info.error);
  } else if (message.type === 'tool_result') {
    call.id = string(content.tool_use_id);
    call.output = readable(content.content) || readable(message.content);
    call.error = content.is_error === true;
  } else if (content.type === 'tool_use' || filled(content.input)) {
    call.id = string(content.id);
    call.tool ||= string(content.name);
    call.input = object(content.input);
  } else {
    // Claude runs recorded before the whole block was kept: the input alone.
    call.input = content;
  }
  call.error ||=
    ['error', 'failed', 'declined', 'cancelled', 'canceled'].includes(call.state) ||
    (call.exit !== undefined && call.exit !== 0);
  return call;
}

const command = (input: Record<string, unknown>) => string(input.command, input.CommandLine);
const filePath = (input: Record<string, unknown>) =>
  string(
    input.file_path,
    input.notebook_path,
    input.path,
    input.file,
    input.TargetFile,
    input.AbsolutePath,
  );
const searched = (input: Record<string, unknown>) =>
  string(input.query, input.pattern, input.globPattern, input.Query, input.Pattern);
const toolName = (tool: string) => tool.replace(/ToolCall$/, '');

export function activityStep(message: RunMessage) {
  const call = toolCall(message);
  const tool = toolName(call.tool);
  const recordedState = call.error
    ? 'Error'
    : call.state
      ? call.state.replace(/_/g, ' ')
      : call.exit !== undefined
        ? `Exit ${call.exit}`
        : message.type === 'tool_result'
          ? 'Result recorded'
          : 'Recorded';
  let label = {
    text: 'Agent text',
    thinking: 'Recorded reasoning',
    status: 'Run status',
    log: 'Run log',
    tool_result: 'Tool result',
    tool_use: tool || 'Tool activity',
  }[message.type];
  let detail = '';
  const run = command(call.input);
  const path = filePath(call.input);
  const query = searched(call.input);
  if (message.type === 'tool_use') {
    if (run) {
      label = 'Run command';
      detail = run;
    } else if (path) {
      label = /^(read|read_file|view_file)$/i.test(tool)
        ? 'Read file'
        : /^(edit|edit_file|multiedit|notebookedit|replace_file_content)$/i.test(tool)
          ? 'Edit file'
          : /^(write|write_file|write_to_file)$/i.test(tool)
            ? 'Write file'
            : tool || 'Tool activity';
      detail = path;
    } else if (query) {
      label = /^(search|grep|glob|find|grep_search|find_by_name)$/i.test(tool)
        ? 'Search'
        : tool || 'Tool activity';
      detail = query;
    }
  } else if (message.type === 'tool_result' && tool) detail = tool;
  const output =
    message.type === 'tool_use' || message.type === 'tool_result'
      ? call.output
      : readable(object(message.content).content) || readable(message.content);
  return { label, detail, state: recordedState, error: call.error, output };
}

export interface ActivityStep {
  key: string;
  messages: RunMessage[];
  title: string;
  label: string;
  detail: string;
  output: string;
  state: string;
  error: boolean;
  tool: boolean;
  running: boolean;
  notebook?: { code: string; language: string; executionCount?: number };
  command?: string;
}
function isStepTitles(message: RunMessage): boolean {
  const data = object(message.content);
  return message.type === 'status' && data.type === 'tool_step_titles' && data.version === 1;
}
function validTitle(value: unknown, supplied = false): string {
  if (typeof value !== 'string') return '';
  const text = value.trim();
  const words = text.split(/\s+/).length;
  return text &&
    Array.from(text).length <= 80 &&
    !/[\r\n<>]/.test(text) &&
    words <= (supplied ? 8 : 6) &&
    (!supplied || words >= 2)
    ? text
    : '';
}
function suppliedDescription(input: Record<string, unknown>): string {
  return validTitle(input.human_description, true) || validTitle(input.description, true);
}
function notebookCall(call: ToolCall): ActivityStep['notebook'] {
  let name = call.tool;
  if (['notebook', 'lyk_door'].includes(call.server ?? '') && !/^mcp(?:__|\.)/.test(name))
    name = `mcp__${call.server}__${name}`;
  let input = call.input;
  if (/^mcp(?:__|\.)lyk_door(?:__|\.)call_tool$/.test(name)) {
    name = string(input.tool);
    if (input.server === 'notebook' && /^execute_[a-z][a-z0-9+_-]*_cell$/.test(name))
      name = `mcp__notebook__${name}`;
    const expectedServer = /^mcp(?:__|\.)notebook(?:__|\.)/.test(name) ? 'notebook' : 'lyk_door';
    if (input.server !== undefined && input.server !== expectedServer) return;
    input = args(input.args ?? input.arguments);
    if (name === 'run_code') name = 'mcp__lyk_door__run_code';
  }
  const match = /^mcp(?:__|\.)notebook(?:__|\.)execute_([a-z][a-z0-9+_-]*)_cell$/i.exec(name);
  const studyCell = /^mcp(?:__|\.)lyk_door(?:__|\.)run_code$/.test(name);
  if (!match && !studyCell) return;
  if (input.server !== undefined || input.arguments !== undefined) {
    const expected = match ? `execute_${match[1]}_cell` : 'run_code';
    if (
      input.server !== (match ? 'notebook' : 'lyk_door') ||
      input.tool !== expected ||
      !filled(input.arguments)
    )
      return;
    input = args(input.arguments);
  }
  const code = executed(input) || command(input);
  if (!code) return;
  const lang = string(input.language, match?.[1], input.kernel) || 'python';
  const count = input.execution_count;
  return {
    code,
    language: lang,
    executionCount:
      typeof count === 'number' && Number.isInteger(count) && count >= 0 ? count : undefined,
  };
}

export function isActivityGroup(steps: readonly ActivityStep[]): boolean {
  const calls = steps.filter((step) => step.tool).length;
  return calls > 1 || (calls === 0 && steps.length > 1);
}

export function activitySteps(messages: readonly RunMessage[]): ActivityStep[] {
  const steps: ActivityStep[] = [];
  const calls = new Map<string, ActivityStep>();
  const titles = new Map<string, string>();
  for (const message of messages.filter(isStepTitles)) {
    const entries = object(message.content).titles;
    if (!Array.isArray(entries)) continue;
    for (const entry of entries) {
      const data = object(entry);
      const title = validTitle(data.title);
      if (Number.isInteger(data.seq) && title) titles.set(`${message.attempt}:${data.seq}`, title);
    }
  }
  for (const message of sorted(messages)) {
    if (isStepTitles(message)) continue;
    const call = toolCall(message);
    const id = call.id ? `${message.attempt}:${call.id}` : '';
    const previous = message.type === 'tool_result' && id ? calls.get(id) : undefined;
    if (previous) {
      previous.messages.push(message);
      previous.output = [previous.output, call.output].filter(Boolean).join('\n');
      previous.error ||= call.error;
      previous.state = previous.error ? 'Error' : 'Completed';
      previous.running = false;
      continue;
    }
    const activity = activityStep(message);
    const tool = message.type === 'tool_use';
    const step: ActivityStep = {
      ...activity,
      key: `${message.attempt}:${message.seq}`,
      messages: [message],
      title:
        titles.get(`${message.attempt}:${message.seq}`) ||
        suppliedDescription(call.input) ||
        suppliedDescription(args(call.input.args ?? call.input.arguments)) ||
        (tool ? toolStepTitle(call.tool, call.input) : activity.label),
      tool,
      notebook: tool ? notebookCall(call) : undefined,
      command: tool ? command(call.input) || undefined : undefined,
      running: tool && !call.output && !call.state && call.exit === undefined,
    };
    steps.push(step);
    if (tool && id) calls.set(id, step);
  }
  return steps;
}

const codeLanguages: Record<string, string> = {
  py: 'python',
  r: 'r',
  jl: 'julia',
  js: 'javascript',
  ts: 'typescript',
  sh: 'shell',
  sql: 'sql',
};
export interface RecordedCode {
  key: string;
  name: string;
  language: string;
  code: string;
}
const extension = (path: string) => path.split('.').pop()?.toLowerCase() ?? '';
// Code a tool call ran: what an MCP execution tool or a notebook cell was given.
function executed(input: Record<string, unknown>) {
  return string(input.code, input.source, input.cell, input.script, input.new_source);
}
// Code a tool call wrote into a source file. Edits record fragments, not files.
function written(input: Record<string, unknown>) {
  return codeLanguages[extension(filePath(input))]
    ? string(input.content, input.fileText, input.CodeContent)
    : '';
}
function language(input: Record<string, unknown>) {
  return (
    string(input.language, input.kernel) ||
    codeLanguages[extension(filePath(input))] ||
    (extension(filePath(input)) === 'ipynb' ? 'python' : '')
  );
}
// Code a run executed or wrote: notebook and script inputs, and source files
// it wrote. Shell commands stay in the execution log. Nothing is recorded as
// code unless a call carried it.
export function recordedCode(messages: readonly RunMessage[]): RecordedCode[] {
  const out: RecordedCode[] = [];
  for (const message of sorted(messages)) {
    if (message.type !== 'tool_use') continue;
    const call = toolCall(message);
    const code = executed(call.input) || written(call.input);
    if (!code) continue;
    out.push({
      key: `${message.attempt}:${message.seq}`,
      name: filePath(call.input) || toolName(call.tool) || 'Script',
      language: language(call.input) || 'text',
      code,
    });
  }
  return out;
}
export interface ExecutionCell {
  key: string;
  label: string;
  detail: string;
  language: string;
  source: string;
  output: string;
  error: boolean;
}
// Each recorded call with the result that answers it: by its id where the
// adapter records one, otherwise the next
// result no call claims by id (Claude runs recorded before ids were kept).
// A Codex call holds its own output.
export function executionCells(messages: readonly RunMessage[]): ExecutionCell[] {
  const ordered = sorted(messages);
  const used = new Set<RunMessage>();
  const claimed = new Set(
    ordered.filter((m) => m.type === 'tool_use').map((m) => `${m.attempt}:${toolCall(m).id}`),
  );
  const cells: ExecutionCell[] = [];
  ordered.forEach((message, index) => {
    if (message.type !== 'tool_use') return;
    const call = toolCall(message);
    const later = ordered.slice(index + 1).filter((m) => m.attempt === message.attempt);
    const result = call.id
      ? later.find((m) => m.type === 'tool_result' && !used.has(m) && toolCall(m).id === call.id)
      : later.find(
          (m) =>
            m.type === 'tool_result' &&
            !used.has(m) &&
            !(toolCall(m).id && claimed.has(`${m.attempt}:${toolCall(m).id}`)),
        );
    if (result) used.add(result);
    const answer = result ? toolCall(result) : undefined;
    const step = activityStep(message);
    const code = executed(call.input) || written(call.input);
    const run = command(call.input);
    cells.push({
      key: `${message.attempt}:${message.seq}`,
      label: step.label,
      detail: step.detail,
      language: code ? language(call.input) || 'text' : run ? 'shell' : 'json',
      source: code || run || recordedContent(call.input),
      output: answer?.output || call.output,
      error: call.error || Boolean(answer?.error),
    });
  });
  return cells;
}

// What a live run is doing, as its latest recorded message says: a call
// still waiting for its result, reasoning, a Codex item it has started, a
// status the CLI reported. Empty when the record says nothing more specific
// than that the run is working.
export function liveStatus(messages: readonly RunMessage[]): string {
  const ordered = sorted(messages);
  const last = ordered.at(-1);
  if (!last) return '';
  const describe = (message: RunMessage) => {
    const call = toolCall(message);
    return `${suppliedDescription(call.input) || toolStepTitle(call.tool, call.input)}…`;
  };
  if (last.type === 'thinking') return 'Thinking…';
  // A Codex call is recorded once complete; the others wait for a result.
  if (last.type === 'tool_use') return filled(object(last.content).item) ? '' : describe(last);
  const content = object(last.content);
  if (last.type === 'status' && content.type === 'blocked_action')
    return `${last.tool || 'A tool call'} was blocked`;
  if (last.type === 'log') {
    const item = object(content.item);
    if (content.type === 'item.started' || content.type === 'item.updated') {
      if (item.type === 'todo_list') return 'Planning…';
      if (item.type === 'reasoning') return 'Thinking…';
      if (item.type === 'command_execution' && string(item.command))
        return describe({ ...last, type: 'tool_use' });
      if (item.type === 'mcp_tool_call') return describe({ ...last, type: 'tool_use' });
    }
    // Claude reports a pause such as compacting its context as a status event.
    if (content.type === 'system' && content.subtype === 'status' && string(content.status))
      return `${string(content.status)[0]!.toUpperCase()}${string(content.status).slice(1)}…`;
  }
  return '';
}
// A minimal nbformat 4 notebook of the recorded cells, for download.
export function notebookJson(cells: readonly ExecutionCell[]): string {
  const lines = (text: string) => text.split(/(?<=\n)/);
  return JSON.stringify(
    {
      nbformat: 4,
      nbformat_minor: 5,
      metadata: {},
      cells: cells.map((cell) => ({
        cell_type: 'code',
        metadata: { lykeion_label: cell.label },
        source: lines(cell.source),
        execution_count: null,
        outputs: cell.output
          ? [
              {
                output_type: 'stream',
                name: cell.error ? 'stderr' : 'stdout',
                text: lines(cell.output),
              },
            ]
          : [],
      })),
    },
    null,
    1,
  );
}

// The divider above a retried run's next attempt: how the earlier one ended.
export function attemptDivider(attempt: number, end: AttemptEnd | undefined): string {
  const on = end?.machine || 'the machine';
  const why: Record<string, string> = {
    runtime_offline: `${on} went offline`,
    timeout: `${on} did not start it in time`,
    provider_network: 'the model provider could not be reached',
    sign_in: `the agent CLI on ${on} was signed out`,
    cli_missing: `the agent CLI was missing on ${on}`,
    cli_too_old: `the agent CLI on ${on} was too old`,
    cli_broken: `the agent CLI on ${on} stopped working`,
    door: `${on} lost its connection to Lykeion`,
    sandbox: `the sandbox on ${on} did not start`,
    fence_breach: "it used a tool outside Lykeion's sources, so its work was discarded",
  };
  return `Attempt ${attempt} — ${why[end?.failure_reason ?? ''] ?? 'it stopped'}; continued below`;
}
