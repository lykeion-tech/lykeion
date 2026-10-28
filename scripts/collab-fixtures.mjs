// CODEMIRROR_STATE may point to a temporary installation of @codemirror/state@6.7.6.
import { createRequire } from 'node:module';
import { writeFileSync } from 'node:fs';
const require = createRequire(import.meta.url);
const { ChangeSet, Text } = process.env.CODEMIRROR_STATE
  ? await import(process.env.CODEMIRROR_STATE) : require('@codemirror/state');
let seed = 0xc011ab;
const random = n => { seed ^= seed << 13; seed ^= seed >>> 17; seed ^= seed << 5; return (seed >>> 0) % n; };
const alphabet = ['a', 'β', '😀', '🧪', '\r', '\n', ' '];
const text = n => Array.from({length: n}, () => alphabet[random(alphabet.length)]).join('');
const boundaries = s => { let n = 0; return [0, ...Array.from(s, c => n += c.length)]; };
const cases = [];
for (let i = 0; i < 500; i++) {
  const doc = text(random(35));
  const points = boundaries(doc), specs = [];
  let at = 0;
  for (let j = 0; j < 1 + random(5); j++) {
    const from = at + random(points.length - at), to = from + random(points.length - from);
    specs.push({from: points[from], to: points[to], insert: Text.of(text(random(9)).split('\n'))});
    at = to;
  }
  const changes = ChangeSet.of(specs, doc.length);
  const first = changes.apply(Text.of(doc.split('\n')));
  const nextPoints = boundaries(first.toString());
  const left = random(nextPoints.length), right = left + random(nextPoints.length - left);
  const second = ChangeSet.of({from:nextPoints[left], to:nextPoints[right], insert:Text.of(text(random(7)).split('\n'))}, first.length);
  const composed = changes.compose(second);
  cases.push({second:second.toJSON(), composedResult:second.apply(first).toString(), composedMaps:Array.from({length:doc.length+1},(_,pos)=>[-1,1].map(assoc=>[pos,assoc,composed.mapPos(pos,assoc)])).flat(), doc, changes: changes.toJSON(), result: changes.apply(Text.of(doc.split('\n'))).toString(),
    maps: Array.from({length: doc.length + 1}, (_, pos) => [-1, 1].map(assoc => [pos, assoc, changes.mapPos(pos, assoc)])).flat()});
}
writeFileSync(new URL('../server/internal/coresearch/doc/testdata/changesets.json', import.meta.url), '[\n' + cases.map(c => JSON.stringify(c)).join(',\n') + '\n]\n');
