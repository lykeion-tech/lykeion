// The run list truncates latest_text, sometimes in the middle of a JSON string.
export function messagePreview(text: string): string {
  if (!/^\s*\{\s*"/.test(text)) return text;
  try {
    const value: unknown = JSON.parse(text);
    return value &&
      typeof value === 'object' &&
      !Array.isArray(value) &&
      typeof (value as Record<string, unknown>).answer === 'string'
      ? String((value as Record<string, unknown>).answer)
      : '';
  } catch {
    const match = /^\s*\{\s*"answer"\s*:\s*"((?:[^"\\]|\\.)*)/.exec(text);
    if (!match) return '';
    let value = match[1] ?? '';
    // The excerpt can end within a JSON escape or Unicode escape.
    for (let n = 0; n < 6; n++) {
      try {
        return JSON.parse('"' + value + '"') as string;
      } catch {
        value = value.slice(0, -1);
      }
    }
    return '';
  }
}

// These are recorded messages, not proof that any message is the accepted report.
export function structuredMessage(text: string): Record<string, unknown> | null {
  try {
    const value: unknown = JSON.parse(text);
    if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
    const record = value as Record<string, unknown>;
    return typeof record.answer === 'string' && typeof record.status === 'string' ? record : null;
  } catch {
    return null;
  }
}

function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (value && typeof value === 'object') {
    const obj = value as Record<string, unknown>;
    return `{${Object.keys(obj)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${canonical(obj[key])}`)
      .join(',')}}`;
  }
  return JSON.stringify(value) ?? 'null';
}
export function matchesAccepted(text: string, handIn: Record<string, unknown>): boolean {
  try {
    return canonical(JSON.parse(text)) === canonical(handIn);
  } catch {
    return false;
  }
}

// member_result is the door's recorded response to an explicit delegation.
export function memberResult(tool: string, output: string) {
  if (!/(?:^|__)member_result$/.test(tool)) return null;
  const [first, ...rest] = output.split('\n\n');
  try {
    const result = JSON.parse(first ?? '') as Record<string, unknown>;
    const record = result.hand_in as Record<string, unknown> | null;
    const handIn = record?.hand_in as Record<string, unknown> | null;
    if (
      result.status !== 'done' ||
      typeof record?.agent_run_id !== 'string' ||
      typeof record?.attempt !== 'number' ||
      !handIn ||
      typeof handIn.answer !== 'string'
    )
      return null;
    return {
      handIn: handIn as Record<string, unknown>,
      agentRunId: record.agent_run_id,
      trailing: rest.join('\n\n').trim(),
    };
  } catch {
    return null;
  }
}
