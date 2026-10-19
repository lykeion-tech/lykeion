import type { RunMessage } from '../api/types';

export interface TranscriptBlock {
  key: string;
  kind: 'text' | 'activity';
  text: string;
  messages: RunMessage[];
}

// Text records are complete adapter messages; only adjacent activity is grouped.
export function transcriptBlocks(messages: readonly RunMessage[]): TranscriptBlock[] {
  const blocks: TranscriptBlock[] = [];
  for (const message of [...messages].sort((a, b) => a.attempt - b.attempt || a.seq - b.seq)) {
    const kind =
      message.type === 'text' && typeof message.content === 'string' ? 'text' : 'activity';
    const previous = blocks.at(-1);
    const text = kind === 'text' ? (message.content as string) : '';
    if (
      kind === 'activity' &&
      previous?.kind === kind &&
      previous.messages[0]?.attempt === message.attempt
    ) {
      previous.messages.push(message);
    } else {
      blocks.push({ key: `${message.attempt}:${message.seq}`, kind, text, messages: [message] });
    }
  }
  return blocks;
}

function object(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
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
    typeof data.content === 'string' ? data.content : undefined,
  );
}
// Claude records the call itself; Codex wraps it in an item.
function toolCall(message: RunMessage) {
  const content = object(message.content);
  const item = Object.keys(object(content.item)).length ? object(content.item) : content;
  const input = Object.keys(object(item.input)).length
    ? object(item.input)
    : Object.keys(object(item.arguments)).length
      ? object(item.arguments)
      : item;
  return { content, item, input };
}
export function activityStep(message: RunMessage) {
  const { content, item, input } = toolCall(message);
  const command = string(input.command, item.command);
  const path = string(input.file_path, input.path, input.file);
  const query = string(input.query, input.pattern);
  const tool = message.tool || string(item.tool, item.name);
  const state = string(item.status, content.status);
  const error =
    content.is_error === true ||
    item.is_error === true ||
    Boolean(item.error) ||
    ['error', 'failed', 'declined', 'cancelled'].includes(state) ||
    (typeof item.exit_code === 'number' && item.exit_code !== 0);
  const recordedState = error
    ? 'Error'
    : state
      ? state.replace(/_/g, ' ')
      : typeof item.exit_code === 'number'
        ? `Exit ${item.exit_code}`
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
  if (message.type === 'tool_use') {
    if (command) {
      label = 'Run command';
      detail = command;
    } else if (path) {
      label = /^(read|read_file)$/i.test(tool)
        ? 'Read file'
        : /^(edit|edit_file)$/i.test(tool)
          ? 'Edit file'
          : /^(write|write_file)$/i.test(tool)
            ? 'Write file'
            : tool || 'Tool activity';
      detail = path;
    } else if (query) {
      label = /^(search|grep|glob|find)$/i.test(tool) ? 'Search' : tool || 'Tool activity';
      detail = query;
    }
  } else if (message.type === 'tool_result' && tool) detail = tool;
  const output =
    readable(item.aggregated_output) ||
    readable(item.output) ||
    readable(item.result) ||
    readable(item.error) ||
    readable(content.content) ||
    readable(message.content);
  return { label, detail, state: recordedState, error, output };
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
// Code a run executed or wrote: notebook and script inputs, and source files
// it wrote. Shell commands stay in the execution log.
export function recordedCode(messages: readonly RunMessage[]): RecordedCode[] {
  const out: RecordedCode[] = [];
  for (const message of messages) {
    if (message.type !== 'tool_use') continue;
    const { item, input } = toolCall(message);
    const path = string(input.file_path, input.path, input.file);
    const extension = path.split('.').pop()?.toLowerCase() ?? '';
    const source = string(input.code, input.source, input.cell, input.script);
    const written = codeLanguages[extension] ? string(input.content, input.new_source) : '';
    const code = source || written;
    if (!code) continue;
    out.push({
      key: `${message.attempt}:${message.seq}`,
      name: path || message.tool || string(item.tool, item.name) || 'Script',
      language: string(input.language, input.kernel) || codeLanguages[extension] || 'python',
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
// Each recorded call with the result that answers it: by tool_use_id when the
// adapter records one, otherwise the next result, or the call's own output.
export function executionCells(messages: readonly RunMessage[]): ExecutionCell[] {
  const sorted = [...messages].sort((a, b) => a.attempt - b.attempt || a.seq - b.seq);
  const used = new Set<RunMessage>();
  const cells: ExecutionCell[] = [];
  sorted.forEach((message, index) => {
    if (message.type !== 'tool_use') return;
    const { content, item, input } = toolCall(message);
    const id = string(content.id, item.id);
    const later = sorted.slice(index + 1);
    const result =
      (id &&
        later.find(
          (m) => m.type === 'tool_result' && string(object(m.content).tool_use_id) === id,
        )) ||
      later.find(
        (m) => m.type === 'tool_result' && !used.has(m) && !string(object(m.content).tool_use_id),
      );
    if (result) used.add(result);
    const step = activityStep(message);
    const answer = result ? activityStep(result) : undefined;
    const code = string(input.code, input.source, input.cell, input.script);
    const command = string(input.command, item.command);
    const own = readable(item.aggregated_output) || readable(item.output) || readable(item.result);
    cells.push({
      key: `${message.attempt}:${message.seq}`,
      label: step.label,
      detail: step.detail,
      language: code
        ? string(input.language, input.kernel) || 'python'
        : command
          ? 'shell'
          : 'json',
      source: code || command || recordedContent(input),
      output: answer?.output || own,
      error: step.error || Boolean(answer?.error),
    });
  });
  return cells;
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
