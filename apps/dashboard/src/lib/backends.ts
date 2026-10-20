export const backends = [
  { id: 'claude', label: 'Claude Code' },
  { id: 'codex', label: 'Codex' },
] as const;

export function backendLabel(harness: string): string {
  return backends.find((backend) => backend.id === harness)?.label ?? harness;
}

export const backendDefaultModel = (_harness: string): string | undefined => undefined;
