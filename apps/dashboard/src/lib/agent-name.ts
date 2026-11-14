/** Format generated agent labels without changing their stored identity. */
export function agentDisplayName(name: string): string {
  const label = name
    .replace(/\b(Team)\s+(?=[a-f\d]{8}\b)(?=[a-f\d]*\d)[a-f\d]{8}\b/gi, '$1')
    .replace(/-(?=[a-f\d]{8}$)(?=[a-f\d]*\d)[a-f\d]{8}$/i, '');
  return label.replace(/^\p{L}/u, (letter) => letter.toLocaleUpperCase());
}

export function agentNameWithoutCli(name: string, harness: string): string {
  const label = agentDisplayName(name);
  if (harness !== 'claude' && harness !== 'codex') return label;
  if (new RegExp(`-${harness}-[a-f\\d]{8}$`, 'i').test(name))
    return label.replace(new RegExp(`-${harness}$`, 'i'), '');
  return label.replace(new RegExp(`(\\bTeam)\\s+${harness}$`, 'i'), '$1');
}
