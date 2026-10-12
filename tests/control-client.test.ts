import {describe,it,expect} from 'vitest';
import {reactive} from 'vue';
import {ControlClient,normalizeEndpoint,validateResponse,ControlError,type Transport} from '../packages/api-client/src/control';
const operation={schema_version:'1',id:'op_test',command_name:'member.invite',state:'succeeded',target_ref:{kind:'workspace',id:'wsp_test'},accepted_at:'2026-10-12T12:00:00Z',result_refs:[],event_cursor:'cursor'};
describe('control client trust boundary',()=>{
 it('rejects credential-bearing or non-local insecure destinations',()=>{
  for(const address of ['http://example.test','https://user:secret@example.test','https://example.test/path','https://example.test/?token=x'])expect(()=>normalizeEndpoint(address)).toThrow();
  expect(normalizeEndpoint('http://127.0.0.1:18431')).toBe('http://127.0.0.1:18431');
 });
 it('rejects drift before it can enter the projection',()=>{
  expect(()=>validateResponse('GET','/v1/workspaces',200,{schema_version:'1',items:[{id:'wsp_test',name:'Lab',revision:1}],event_cursor:'cursor',snapshot_revision:'1'})).toThrow('incompatible');
  expect(()=>validateResponse('POST','/v1/workspaces/wsp_test/commands/member.invite',202,{...operation,secret:'unexpected'})).toThrow('incompatible');
 });
 it('retries an ambiguous transport result with the exact original body',async()=>{
  const bodies:unknown[]=[];let calls=0;
  const transport:Transport=async(_method,_path,body)=>{bodies.push(structuredClone(body));if(calls++===0)throw new Error('connection lost');return {status:202,body:operation};};
  const client=new ControlClient(transport);
  await expect(client.command({id:'wsp_test',name:'Lab',revision:'1'},'member.invite',{destination:'usr_one'},[],'same-key')).rejects.toThrow('connection lost');
  await client.command({id:'wsp_test',name:'Lab',revision:'2'},'member.invite',{destination:'usr_two'},[],'same-key');
  expect(bodies[1]).toEqual(bodies[0]);
 });
 it('serializes reactive subject references without losing their versions',async()=>{
  let sent:unknown;const client=new ControlClient(async(_method,_path,body)=>{sent=body;return {status:202,body:{...operation,command_name:'decision.respond'}};});
  const subjects=reactive([{kind:'membership',id:'mem_test',revision:'3'}]);
  await client.command({id:'wsp_test',name:'Lab',revision:'4'},'decision.respond',{decision_id:'dec_test',decision_revision:'1',choice_id:'approve',subject_versions:subjects});
  expect(sent).toMatchObject({payload:{subject_versions:[{id:'mem_test',revision:'3'}]}});
 });
 it('keeps problem status available to clear revoked projections',async()=>{
  const client=new ControlClient(async()=>({status:401,body:{schema_version:'1',code:'SESSION_EXPIRED',message:'Session expired',request_id:'req_one',retryable:false,violations:[],current_versions:[]}}));
  await expect(client.session()).rejects.toMatchObject({status:401,code:'SESSION_EXPIRED'} satisfies Partial<ControlError>);
 });
});
