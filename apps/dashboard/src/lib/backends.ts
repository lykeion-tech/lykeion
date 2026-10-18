export const backends = [
  { id: 'claude', label: 'Claude Code' },
  { id: 'codex', label: 'Codex' },
  { id: 'cursor', label: 'Cursor' },
  { id: 'antigravity', label: 'Antigravity' },
] as const;

export function backendLabel(harness: string): string {
  return backends.find((backend) => backend.id === harness)?.label ?? harness;
}

export const backendDefaultModel = (harness: string) =>
  (({ cursor: 'gpt-5', antigravity: 'gemini-3-pro' }) as Record<string, string>)[harness];
