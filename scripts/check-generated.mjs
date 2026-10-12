import {spawnSync} from 'node:child_process'
import fs from 'node:fs'
import {createHash} from 'node:crypto'
const files=['contracts/control.openapi.json','contracts/realtime.protocol.json','contracts/schemas/common.json','packages/api-client/src/generated/control.ts','science/src/lykeion_science/control_models.py']
const hash=p=>createHash('sha256').update(fs.readFileSync(p)).digest('hex')
const before=new Map(files.map(p=>[p,hash(p)]))
for(const [bin,args] of [
 ['python3',['scripts/generate-contracts.py']],
 ['node_modules/.bin/openapi-typescript',['contracts/control.openapi.json','-o','packages/api-client/src/generated/control.ts']],
 ['science/.venv/bin/datamodel-codegen',['--input','contracts/control.openapi.json','--input-file-type','openapi','--output','science/src/lykeion_science/control_models.py','--output-model-type','pydantic_v2.BaseModel','--target-python-version','3.14','--disable-timestamp','--use-standard-collections','--use-union-operator','--formatters','black','isort']]
]) {const r=spawnSync(bin,args,{stdio:'inherit'});if(r.status!==0)process.exit(r.status??1)}
const changed=files.filter(p=>hash(p)!==before.get(p));if(changed.length){console.error('Regenerated drift; review and commit:',changed);process.exit(1)}
console.log('Generated contracts and TypeScript/Python models are reproducible.')
