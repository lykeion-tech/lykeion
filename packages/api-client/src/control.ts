import Ajv from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';
import spec from '../../../contracts/control.openapi.json';
import type { components } from './generated/control';
export type Model<K extends keyof components['schemas']> = components['schemas'][K];
export interface Page<T> {schema_version:'1';items:T[];snapshot_revision:string;event_cursor:string;next_cursor?:string}
export interface Read<T> {schema_version:'1';data:T;event_cursor:string}
export type Transport = (method:'GET'|'POST'|'PUT', path:string, body?:unknown, key?:string, csrf?:string)=>Promise<{status:number;body:unknown}>;
const ajv = new Ajv({strict:true}); addFormats(ajv);
const convert = (value: unknown) => JSON.parse(JSON.stringify(value).replaceAll('#/components/schemas/','#/$defs/'));
const definitions = convert(spec.components.schemas);
const validators = new Map<string, ReturnType<typeof ajv.compile>>();
export function validateResponse(method:string,path:string,status:number,body:unknown) {
  const pathname=path.split('?')[0]!;
  const route=Object.entries(spec.paths).find(([template])=>new RegExp('^'+template.replaceAll(/\{[^}]+\}/g,'[^/]+')+'$').test(pathname));
  const operation=route?.[1][method.toLowerCase() as keyof typeof route[1]] as {responses:Record<string,{content?:{'application/json':{schema:unknown}}}>}|undefined;
  if(!operation) throw new Error('Unsupported control API route.');
  const schema=(operation.responses[String(status)]??operation.responses.default)?.content?.['application/json'].schema;
  if(!schema) {if(status===204)return;throw new Error('Unsupported control API response.');}
  const key=method+route![0]+status;
  let check=validators.get(key);if(!check){check=ajv.compile({$defs:definitions,...convert(schema)});validators.set(key,check);}
  if(!check(body)) throw new Error('The control service returned an incompatible response.');
}
export class ControlError extends Error {constructor(public status:number,public code:string,message:string){super(message)}}
export function normalizeEndpoint(value:string) {
 const url=new URL(value);if(url.username||url.password||url.search||url.hash||url.pathname!=='/')throw new Error('Use an API origin without credentials or a path.');
 if(url.protocol!=='https:' && !(url.protocol==='http:'&&['127.0.0.1','localhost','[::1]'].includes(url.hostname)))throw new Error('The API requires HTTPS or a loopback address.');
 return url.origin;
}
export function browserTransport(endpoint:string):Transport {
 const origin=normalizeEndpoint(endpoint);
 return async(method,path,body,key,csrf)=>{
  if(!path.startsWith('/v1/')||path.includes('..')||path.includes('\\'))throw new Error('Invalid API path.');
  const response=await fetch(origin+path,{method,credentials:'include',redirect:'error',signal:AbortSignal.timeout(15000),headers:{'Content-Type':'application/json',...(key?{'Idempotency-Key':key}:{}),...(csrf?{'X-CSRF-Token':csrf}:{})},body:body===undefined?undefined:JSON.stringify(body)});
  const text=await response.text();if(text.length>2_000_000)throw new Error('API response exceeded the size limit.');
  return {status:response.status,body:text?JSON.parse(text):undefined};
 };
}
export class ControlClient {
 csrf='';
 private pending=new Map<string,{workspace:string;name:string;body:unknown}>();
 constructor(readonly transport:Transport){}
 async call<T>(method:'GET'|'POST'|'PUT',path:string,body?:unknown,key?:string):Promise<T>{
  const response=await this.transport(method,path,body,key,this.csrf);
  validateResponse(method,path,response.status,response.body);
  if(response.status>=400){const p=response.body as Model<'Problem'>;throw new ControlError(response.status,p.code,p.message);}
  return response.body as T;
 }
 async session(code?:string){const r=code?await this.call<Read<Model<'Session'>>>('POST','/v1/auth/exchange',{code}):await this.call<Read<Model<'Session'>>>('GET','/v1/session');this.csrf=r.data.csrf_token;return r.data;}
 async all<K extends keyof components['schemas']>(path:string):Promise<Page<Model<K>>>{
  let first=await this.call<Page<Model<K>>>('GET',path);let cursor=first.next_cursor;const seen=new Set<string>();
  while(cursor){if(seen.has(cursor)||seen.size>=100)throw new Error('Pagination did not finish.');seen.add(cursor);const page=await this.call<Page<Model<K>>>('GET',path+'?cursor='+encodeURIComponent(cursor));if(page.snapshot_revision!==first.snapshot_revision)throw new ControlError(409,'SNAPSHOT_EXPIRED','Workspace changed. Refresh and try again.');first={...page,items:[...first.items,...page.items]};cursor=page.next_cursor;}
  return first;
 }
 command(workspace:Model<'Workspace'>,name:string,payload:unknown,versions:Model<'Ref'>[]=[],key:string=crypto.randomUUID()){
  let request=this.pending.get(key);
  if(request&&(request.workspace!==workspace.id||request.name!==name))throw new Error('Command key belongs to another action.');
  if(!request){request={workspace:workspace.id,name,body:JSON.parse(JSON.stringify({schema_version:'1',target:{kind:'workspace',id:workspace.id},expected_versions:[{kind:'workspace',id:workspace.id,revision:workspace.revision},...versions],payload}))};if(this.pending.size>=100)throw new Error('Too many pending actions. Reconnect after checking operation receipts.');this.pending.set(key,request);}
  return this.call<Model<'Operation'>>('POST',`/v1/workspaces/${workspace.id}/commands/${name}`,request.body,key).then(result=>{this.pending.delete(key);return result;}).catch(error=>{if(error instanceof ControlError&&error.status<500)this.pending.delete(key);throw error;});
 }
}
