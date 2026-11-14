interface Token {
  value: string;
  quoted: boolean;
}
export const ELEMENTS = new Set(
  'H He Li Be B C N O F Ne Na Mg Al Si P S Cl Ar K Ca Sc Ti V Cr Mn Fe Co Ni Cu Zn Ga Ge As Se Br Kr Rb Sr Y Zr Nb Mo Tc Ru Rh Pd Ag Cd In Sn Sb Te I Xe Cs Ba La Ce Pr Nd Pm Sm Eu Gd Tb Dy Ho Er Tm Yb Lu Hf Ta W Re Os Ir Pt Au Hg Tl Pb Bi Po At Rn Fr Ra Ac Th Pa U Np Pu Am Cm Bk Cf Es Fm Md No Lr Rf Db Sg Bh Hs Mt Ds Rg Cn Nh Fl Mc Lv Ts Og'.split(
    ' ',
  ),
);
function tokenize(text: string): Token[] | undefined {
  const tokens: Token[] = [];
  let i = 0,
    lineStart = true;
  while (i < text.length) {
    const c = text[i]!;
    if (/\s/.test(c)) {
      lineStart = c === '\n' ? true : lineStart;
      i++;
      continue;
    }
    if (c === '#') {
      while (i < text.length && text[i] !== '\n') i++;
      continue;
    }
    if (c === ';' && lineStart) {
      i++;
      const start = i;
      let end = -1;
      while (i < text.length) {
        if (text[i] === '\n' && text[i + 1] === ';') {
          end = i;
          i += 2;
          break;
        }
        i++;
      }
      if (end < 0 || (i < text.length && text[i] !== '\n' && text[i] !== '\r')) return undefined;
      tokens.push({ value: text.slice(start, end), quoted: true });
      lineStart = false;
    } else if (c === "'" || c === '"') {
      i++;
      const start = i;
      while (
        i < text.length &&
        !(text[i] === c && (i + 1 === text.length || /\s|#/.test(text[i + 1]!)))
      ) {
        if (text[i] === '\n') return undefined;
        i++;
      }
      if (i >= text.length) return undefined;
      tokens.push({ value: text.slice(start, i), quoted: true });
      i++;
      lineStart = false;
    } else {
      const start = i;
      while (i < text.length && !/\s/.test(text[i]!)) i++;
      tokens.push({ value: text.slice(start, i), quoted: false });
      lineStart = false;
    }
    if (tokens.length > 100000) return undefined;
  }
  return tokens;
}
const reserved = (t: Token) =>
  !t.quoted && /^_|^(?:loop_|stop_|global_|save_|data_)/i.test(t.value);
export function cifGeometry(
  text: string,
): { text: string; format: string; atoms: number } | undefined {
  const tokens = tokenize(text);
  if (!tokens?.length || !/^data_.+/i.test(tokens[0]!.value)) return undefined;
  let i = 1,
    atomLoop: string[][] | undefined,
    atomTags: string[] = [];
  while (i < tokens.length) {
    const t = tokens[i++]!;
    if (!t.quoted && t.value.toLowerCase() === 'loop_') {
      const tags: string[] = [];
      while (i < tokens.length && !tokens[i]!.quoted && tokens[i]!.value.startsWith('_'))
        tags.push(tokens[i++]!.value.toLowerCase());
      if (!tags.length || new Set(tags).size !== tags.length) return undefined;
      const values: string[] = [];
      while (i < tokens.length && !reserved(tokens[i]!)) values.push(tokens[i++]!.value);
      if (!values.length || values.length % tags.length) return undefined;
      if (tags.some((tag) => tag.startsWith('_atom_site.'))) {
        if (atomLoop || values.length / tags.length > 1000) return undefined;
        atomTags = tags;
        atomLoop = [];
        for (let j = 0; j < values.length; j += tags.length)
          atomLoop.push(values.slice(j, j + tags.length));
      }
    } else if (!t.quoted && t.value.startsWith('_')) {
      if (i >= tokens.length || reserved(tokens[i]!)) return undefined;
      i++;
    } else return undefined;
  }
  if (!atomLoop?.length) return undefined;
  const col = (tag: string) => atomTags.indexOf(`_atom_site.${tag}`);
  const type = col('type_symbol'),
    x = col('cartn_x'),
    y = col('cartn_y'),
    z = col('cartn_z'),
    alt = col('label_alt_id'),
    model = col('pdbx_pdb_model_num');
  if ([type, x, y, z].some((n) => n < 0)) return undefined;
  const result: string[] = [];
  let modelName: string | undefined;
  for (const row of atomLoop) {
    const symbolRaw = row[type]!,
      symbol = symbolRaw[0]?.toUpperCase() + symbolRaw.slice(1).toLowerCase();
    if (!ELEMENTS.has(symbol) || (alt >= 0 && !['.', '?'].includes(row[alt]!))) return undefined;
    if (model >= 0) {
      const current = row[model]!;
      if (!/^\d+$/.test(current) || (modelName && modelName !== current)) return undefined;
      modelName = current;
    }
    const coords = [x, y, z].map((n) => row[n]!);
    if (
      coords.some(
        (v) =>
          !/^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/.test(v) ||
          !Number.isFinite(Number(v)) ||
          Math.abs(Number(v)) > 1e6,
      )
    )
      return undefined;
    result.push(`${symbol} ${coords.join(' ')}`);
  }
  return {
    text: `${result.length}\nCartesian CIF atoms; bonds inferred\n${result.join('\n')}\n`,
    format: 'xyz',
    atoms: result.length,
  };
}
