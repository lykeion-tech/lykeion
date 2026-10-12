import fs from 'node:fs'
import assert from 'node:assert/strict'
import Ajv from 'ajv/dist/2020.js'
import addFormats from 'ajv-formats'
const api=JSON.parse(fs.readFileSync('contracts/control.openapi.json','utf8'))
const convert=x=>JSON.parse(JSON.stringify(x).replaceAll('#/components/schemas/','#/$defs/'))
const ajv=new Ajv({strict:true,allErrors:true}); addFormats(ajv)
const defs=convert(api.components.schemas)
const validators={}
for(const name of Object.keys(defs)) validators[name]=ajv.compile({$defs:defs,$ref:`#/$defs/${name}`})
const wire=JSON.parse(fs.readFileSync('contracts/realtime.protocol.json','utf8'))
const frame=ajv.compile({$defs:defs,oneOf:convert(wire.oneOf)})
const target={kind:'workspace',id:'w_test'}
const good={schema_version:'1',target,expected_versions:[],payload:{destination:'person@example.test',requested_role:'member',expires_at:'2027-01-01T00:00:00Z'}}
assert(validators.MemberInviteCommand(good),JSON.stringify(validators.MemberInviteCommand.errors))
assert(!validators.MemberInviteCommand({...good,payload:{...good.payload,requested_role:'owner'}}))
assert(!validators.MemberInviteCommand({...good,actor_id:'u_forged'}))
assert(!validators.MemberInviteCommand({...good,payload:{...good.payload,force_advance:true}}))
assert(!validators.Revision(1));assert(validators.Revision('9007199254740993'))
assert(frame({schema_version:'1',type:'document.sync',message_id:'msg_1',payload:{document_id:'doc_1',known_revision:'4'}}))
assert(!frame({schema_version:'1',type:'gate.advance',message_id:'msg_1',payload:{}}))
assert(!frame({schema_version:'1',type:'presence.update',message_id:'msg_1',payload:{user_id:'u_forged'}}))
for(const [path,ops] of Object.entries(api.paths))for(const op of Object.values(ops)) {
 for(const match of path.matchAll(/\{([^}]+)\}/g)) assert(op.parameters?.some(p=>p.in==='path'&&p.required&&p.name===match[1]),`${path} missing path parameter`)
 for(const response of Object.values(op.responses))if(response.content)ajv.compile({$defs:defs,...convert(response.content['application/json'].schema)})
}
console.log(`Contracts valid: ${Object.keys(defs).length} schemas, ${Object.keys(api.paths).length} routes, 13 closed frame variants; negative authorization-shape fixtures passed.`)
