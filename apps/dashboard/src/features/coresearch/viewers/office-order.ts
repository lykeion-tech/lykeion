export function orderSlides(
  ids: string[],
  rels: { id: string; target: string; external?: boolean }[],
): string[] | undefined {
  const byId = new Map<string, { id: string; target: string; external?: boolean }>();
  for (const rel of rels) {
    if (!rel.id || byId.has(rel.id)) return;
    byId.set(rel.id, rel);
  }
  const result: string[] = [];
  const targets = new Set<string>();
  for (const id of ids) {
    const rel = byId.get(id);
    if (!rel || rel.external || !/^slides\/slide[1-9]\d*\.xml$/.test(rel.target)) return;
    if (targets.has(rel.target)) return;
    targets.add(rel.target);
    result.push(`ppt/${rel.target}`);
  }
  return result;
}
