export interface AlignmentRow {
  name: string;
  sequence: string;
}
export interface TreeNode {
  name: string;
  children: TreeNode[];
}

// These limits bound both the parser and the rendered DOM; refuse incomplete input
// rather than representing a partial topology or alignment as complete.
export function parseAlignment(text: string, extension: string, partial: boolean) {
  if (partial) return undefined;
  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  const rows = new Map<string, string>();
  const add = (name: string, seq: string) => {
    if (!name || name.length > 200 || !/^[A-Za-z.*?\-]+$/.test(seq)) return false;
    if (!rows.has(name) && rows.size >= 100) return false;
    const next = (rows.get(name) ?? '') + seq;
    if (next.length > 20000) return false;
    rows.set(name, next);
    return true;
  };
  if (['phy', 'phylip'].includes(extension)) {
    const header = lines
      .shift()
      ?.trim()
      .match(/^(\d+)\s+(\d+)$/);
    if (!header) return undefined;
    const count = Number(header[1]),
      width = Number(header[2]);
    if (!count || count > 100 || !width || width > 20000) return undefined;
    for (const line of lines) {
      if (!line.trim()) continue;
      const match = line.trim().match(/^(\S+)\s+([A-Za-z.*?\-]+)$/);
      if (!match || rows.has(match[1]!) || !add(match[1]!, match[2]!)) return undefined;
    }
    if (rows.size !== count || [...rows.values()].some((seq) => seq.length !== width))
      return undefined;
  } else {
    const first = lines.shift()?.trim() ?? '';
    const stockholm = ['sto', 'stk'].includes(extension);
    if (!(stockholm ? /^#\s*STOCKHOLM\b/i : /^(CLUSTAL|MUSCLE)\b/i).test(first)) return undefined;
    let terminated = false;
    for (const line of lines) {
      if (terminated) {
        if (line.trim()) return undefined;
        continue;
      }
      if (line.trim() === '//') {
        terminated = true;
        continue;
      }
      if (!line.trim() || line.startsWith('#') || /^\s+[*:. ]+\s*$/.test(line)) continue;
      const match = line.trim().match(/^(\S+)\s+([A-Za-z.*?\-]+)(?:\s+\d+)?$/);
      if (!match || !add(match[1]!, match[2]!)) return undefined;
    }
    if (stockholm && !terminated) return undefined;
  }
  const values = [...rows.entries()].map(([name, sequence]) => ({ name, sequence }));
  if (!values.length || values.some((row) => row.sequence.length !== values[0]!.sequence.length))
    return undefined;
  return {
    rows: values.map((row) => ({ ...row, sequence: row.sequence.slice(0, 300) })),
    width: values[0]!.sequence.length,
    truncated: values[0]!.sequence.length > 300,
  };
}

export function parseTree(text: string, partial: boolean): TreeNode | undefined {
  if (partial || text.length > 200000) return undefined;
  let i = 0,
    count = 0;
  const skip = () => {
    while (/\s/.test(text[i] ?? '') && i < text.length) i++;
  };
  const node = (depth: number): TreeNode => {
    if (depth > 60 || ++count > 300) throw Error('tree too large');
    skip();
    const children: TreeNode[] = [];
    if (text[i] === '(') {
      i++;
      do {
        children.push(node(depth + 1));
        skip();
        if (text[i] !== ',') break;
        i++;
      } while (true);
      if (text[i++] !== ')' || children.length < 2) throw Error('invalid tree');
    }
    skip();
    let name = '';
    if (text[i] === "'") {
      i++;
      let closed = false;
      while (i < text.length) {
        if (text[i] === "'") {
          i++;
          if (text[i] === "'") {
            name += "'";
            i++;
          } else {
            closed = true;
            break;
          }
        } else name += text[i++];
        if (name.length > 200) throw Error('label too long');
      }
      if (!closed) throw Error('unclosed label');
    } else {
      const start = i;
      while (i < text.length && !/[\s(),:;\[\]]/.test(text[i]!)) i++;
      name = text.slice(start, i);
      if (name.length > 200) throw Error('label too long');
    }
    if (!name && !children.length) throw Error('missing leaf');
    skip();
    // NHX comments are metadata, not markup; skip rather than interpret them.
    while (text[i] === '[') {
      const end = text.indexOf(']', i + 1);
      if (end < 0) throw Error('comment');
      i = end + 1;
      skip();
    }
    if (text[i] === ':') {
      i++;
      const match = text.slice(i).match(/^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?/);
      if (!match) throw Error('length');
      i += match[0].length;
      skip();
    }
    while (text[i] === '[') {
      const end = text.indexOf(']', i + 1);
      if (end < 0) throw Error('comment');
      i = end + 1;
      skip();
    }
    return { name, children };
  };
  try {
    skip();
    const rooted = text.slice(i).match(/^\[&[RU]\]/i);
    if (rooted) i += rooted[0].length;
    const root = node(0);
    skip();
    if (text[i++] !== ';') return undefined;
    skip();
    return i === text.length ? root : undefined;
  } catch {
    return undefined;
  }
}

export function treeViewportWidth(nodes: { x: number; name: string }[]) {
  return Math.max(220, ...nodes.map((node) => node.x + node.name.length * 16 + 20));
}
