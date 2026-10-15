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
export function activityStep(message: RunMessage) {
  const content = object(message.content);
  const item = Object.keys(object(content.item)).length ? object(content.item) : content;
  const input = Object.keys(object(item.input)).length
    ? object(item.input)
    : Object.keys(object(item.arguments)).length
      ? object(item.arguments)
      : item;
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
