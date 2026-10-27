export function childPath(parent: string, key: string | number): string {
  return typeof key === 'number' ? `${parent}[${key}]` : `${parent}.${key}`;
}

/** Every path whose key or scalar value contains `query` (case-insensitive),
 *  with each match's ancestors, so the tree can open straight to it. */
export function jsonPathsMatching(value: unknown, query: string): Set<string> {
  const found = new Set<string>();
  const q = query.trim().toLowerCase();
  if (q === '') return found;
  const visit = (node: unknown, path: string, ancestors: string[], key?: string | number): void => {
    const keyHit = key !== undefined && String(key).toLowerCase().includes(q);
    const isObject = node !== null && typeof node === 'object';
    const valueHit = !isObject && String(node).toLowerCase().includes(q);
    if (keyHit || valueHit) for (const p of [...ancestors, path]) found.add(p);
    if (!isObject) return;
    const entries: [string | number, unknown][] = Array.isArray(node)
      ? node.map((v, i) => [i, v])
      : Object.entries(node as Record<string, unknown>);
    for (const [k, v] of entries) visit(v, childPath(path, k), [...ancestors, path], k);
  };
  visit(value, '$', []);
  return found;
}
