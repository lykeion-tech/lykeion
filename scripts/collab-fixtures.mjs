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

// Keep a real outstanding edit for each independently observed client. Only
// the selected client receives server steps and rebases its draft after 409.
seed = 782;
const initial = 'a😀\r\nβ🧪 paper\r\nend';
const draftFor = (source, forced) => {
  const points = boundaries(source);
  let kind = forced?.kind ?? ['insert', 'delete', 'replace'][random(3)];
  let left = forced?.left ?? random(points.length);
  if (left === points.length - 1) kind = 'insert';
  const right = kind === 'insert' ? left : left + 1 + random(Math.min(6, points.length - left - 1));
  const spec = {from: points[left], to: points[right], insert: kind === 'delete' ? '' : text(1 + random(7))};
  if (forced) Object.assign(spec, forced.spec);
  return {kind, ...spec, changes: ChangeSet.of({...spec, insert: Text.of(spec.insert.split('\n'))}, source.length)};
};
const clients = [
  {version: 0, text: initial, draft: draftFor(initial, {kind:'insert',left:0,spec:{from:0,to:0,insert:'😀\r\n'}})},
  {version: 0, text: initial, draft: draftFor(initial, {kind:'delete',left:0,spec:{from:0,to:1,insert:''}})},
  {version: 0, text: initial, draft: draftFor(initial, {kind:'replace',left:0,spec:{from:0,to:3,insert:'β🧪\r\n'}})},
];
const plainDraft = d => ({kind:d.kind, from:d.from, to:d.to, insert:d.insert, changes:d.changes.toJSON()});
const initialClients = clients.map(c => plainDraft(c.draft));
let serverText = Text.of(initial.split('\n'));
const journal = [], trace = [], tracked = [];
let rangeID = 0;
const stats = {staleAttempts:0, changedRebases:0, overlappingRebases:0, insert:0, delete:0, replace:0};
const overlaps = (change, range) => {
  let touched = false;
  change.iterChangedRanges((from,to) => {
    if (range.from === range.to) touched ||= from < range.from && to > range.to;
    else touched ||= (from === to && from > range.from && from < range.to) || (from < range.to && to > range.from);
  });
  return touched;
};
for (let round = 0; round < 100; round++) {
  const order = [0,1,2]; // Deterministic Fisher-Yates order after the first overlap.
  if (round !== 0) for (let n = order.length - 1; n > 0; n--) {const j=random(n+1);[order[n],order[j]]=[order[j],order[n]];}
  for (const writer of order) {
    const client = clients[writer], beforeVersion = journal.length;
    stats[client.draft.kind]++;
    let mapped = client.draft.changes, observed = Text.of(client.text.split('\n'));
    const rebases = [];
    if (client.version !== beforeVersion) stats.staleAttempts++;
    for (let version = client.version; version < beforeVersion; version++) {
      const remote = journal[version];
      mapped.iterChangedRanges((from,to) => {if (remote.touchesRange(from,to)) stats.overlappingRebases++;});
      const previous = JSON.stringify(mapped.toJSON());
      mapped = mapped.map(remote);
      if (JSON.stringify(mapped.toJSON()) !== previous) stats.changedRebases++;
      observed = remote.apply(observed);
      rebases.push({version:version+1, remote:remote.toJSON(), changes:mapped.toJSON(), observed:observed.toString(), localResult:mapped.apply(observed).toString()});
    }
    if (observed.toString() !== serverText.toString()) throw new Error('client did not catch up');
    const source = serverText;
    for (const range of tracked) {
      if (overlaps(mapped,range)) range.status='stale';
      range.from=mapped.mapPos(range.from,1);
      range.to=Math.max(range.from,mapped.mapPos(range.to,-1));
    }
    serverText=mapped.apply(serverText);
    const added=[];
    if (writer===2) mapped.iterChanges((fromA,toA,fromB,toB,insert) => {
      const range={id:rangeID++,from:fromB,to:toB,inserted:insert.toString(),removed:source.sliceString(fromA,toA),status:'pending'};
      tracked.push(range);added.push({...range});
    });
    for(const range of tracked) if(range.status==='pending' && serverText.sliceString(range.from,range.to)!==range.inserted) throw new Error('pending reference range drifted');
    journal.push(mapped);
    const nextDraft=draftFor(serverText.toString());
    trace.push({writer,base:client.version,observed:client.text,draft:plainDraft(client.draft),rebases,accepted:mapped.toJSON(),result:serverText.toString(),added,ranges:tracked.map(({id,from,to,status})=>({id,from,to,status})),nextDraft:plainDraft(nextDraft)});
    client.version=journal.length;client.text=serverText.toString();client.draft=nextDraft;
  }
}
writeFileSync(new URL('../server/internal/coresearch/doc/testdata/interleavings.json',import.meta.url), JSON.stringify({seed:782,initial,initialClients,stats,trace})+'\n');

// Decisions participate in the same journal that stale clients must rebase over.
seed=1782;
const decisionInitial='first😀\r\nsecond🧪\r\nthird';
let decisionText=Text.of(decisionInitial.split('\n'));
const decisionJournal=[],decisionRanges=[],decisionTrace=[];
const decisionClients=[0,1,2].map(()=>({version:0,text:decisionInitial,draft:draftFor(decisionInitial)}));
const decisionInitialClients=decisionClients.map(c=>plainDraft(c.draft));
let decisionRangeID=0;
const mapDecisionRanges=change=>{for(const range of decisionRanges){if(!['pending','stale'].includes(range.status))continue;if(overlaps(change,range))range.status='stale';range.from=change.mapPos(range.from,1);range.to=Math.max(range.from,change.mapPos(range.to,-1));}};
for(let i=0;i<90;i++){
 const writer=i%3,client=decisionClients[writer],rebases=[];let mapped=client.draft.changes,observed=Text.of(client.text.split('\n'));
 for(let version=client.version;version<decisionJournal.length;version++){const remote=decisionJournal[version];mapped=mapped.map(remote);observed=remote.apply(observed);rebases.push({version:version+1,remote:remote.toJSON(),changes:mapped.toJSON(),observed:observed.toString(),localResult:mapped.apply(observed).toString()});}
 const source=decisionText;mapDecisionRanges(mapped);decisionText=mapped.apply(decisionText);decisionJournal.push(mapped);
 const added=[];if(writer===2)mapped.iterChanges((fromA,toA,fromB,toB,insert)=>{const range={id:decisionRangeID++,from:fromB,to:toB,inserted:insert.toString(),removed:source.sliceString(fromA,toA),status:'pending'};decisionRanges.push(range);added.push({...range});});
 const nextDraft=draftFor(decisionText.toString());
 decisionTrace.push({kind:'edit',writer,version:decisionJournal.length,base:client.version,observed:client.text,draft:plainDraft(client.draft),rebases,accepted:mapped.toJSON(),result:decisionText.toString(),added,ranges:decisionRanges.map(r=>({...r})),nextDraft:plainDraft(nextDraft)});
 client.version=decisionJournal.length;client.text=decisionText.toString();client.draft=nextDraft;
 if(i%3===2){
  const actionable=decisionRanges.filter(r=>r.status==='pending'||(r.status==='stale'&&r.from===r.to));if(!actionable.length)continue;
  const target=actionable[random(actionable.length)],keep=target.status==='pending'&&random(2)===0;
  let changes=null;
  if(!keep){changes=ChangeSet.of({from:target.from,to:target.to,insert:Text.of(target.removed.split('\n'))},decisionText.length);mapDecisionRanges(changes);decisionText=changes.apply(decisionText);decisionJournal.push(changes);}
  target.status=keep?'kept':'undone';
  decisionTrace.push({kind:keep?'keep':'undo',writer:random(2),change:target.id,version:decisionJournal.length,accepted:changes?.toJSON(),result:decisionText.toString(),ranges:decisionRanges.map(r=>({...r}))});
 }
}
writeFileSync(new URL('../server/internal/coresearch/doc/testdata/decisions.json',import.meta.url),JSON.stringify({seed:1782,initial:decisionInitial,initialClients:decisionInitialClients,trace:decisionTrace})+'\n');
