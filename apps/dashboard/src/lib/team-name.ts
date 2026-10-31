export function teamDisplayParts(name: string): { label: string; cli?: 'claude' | 'codex' } {
  const match = /^(.*\bTeam)\s+([a-f\d]{8})\s+(claude|codex)$/i.exec(name);
  if (!match || !/\d/.test(match[2]!)) return { label: name };
  return { label: match[1]!, cli: match[3]!.toLowerCase() as 'claude' | 'codex' };
}
