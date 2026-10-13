import type { IpcMainInvokeEvent, BrowserWindow } from 'electron';
import WebSocket from 'ws';
import type {SessionVault} from './session-vault.js';
import {randomUUID} from 'node:crypto';

// The destination is chosen by the launcher, never by renderer IPC.
export function controlBridge(getWindow:()=>BrowserWindow|null, endpoint:string,vault?:SessionVault) {
 const url=new URL(endpoint);
 if(url.username||url.password||url.pathname!=='/'||url.search||url.hash || (url.protocol!=='https:'&&!(url.protocol==='http:'&&['127.0.0.1','localhost','[::1]'].includes(url.hostname))))throw new Error('Invalid control origin');
let stream:WebSocket|undefined;let streamID='';let frames:unknown[]=[];let closed=true;
 let cookie=vault?.load()??'';let heartbeat:ReturnType<typeof setInterval>|undefined;
 function authorize(event:IpcMainInvokeEvent){const window=getWindow();if(event.sender!==window?.webContents||event.senderFrame!==window.webContents.mainFrame)throw new Error('Invalid sender');}
 const close=()=>{clearInterval(heartbeat);stream?.terminate();stream=undefined;frames=[];closed=true;};
 const id='[a-z][a-z0-9_-]{2,127}';
 const read=new RegExp(`^/v1/(session|capabilities|workspaces|me/operations/${id}|workspaces/${id}/(members|membership-decisions|programmes(?:/${id})?|access-grants|invitations|events|artifacts/${id}(?:/content)?|operations/${id}|invitations/${id}/proof))$`);
 const put=new RegExp(`^/v1/workspaces/${id}/uploads/${id}/parts/1$`);
 const post=new RegExp(`^/v1/(auth/(exchange|logout)|invitations/${id}/accept|workspaces/${id}/uploads(?:/${id}/complete)?|workspaces/${id}/commands/(artifact\\.submit|programme\\.create|access\\.change|preference\\.update|member\\.invite|member\\.remove|member\\.leave|invitation\\.revoke|member\\.request-access-change|workspace\\.request-owner-transfer|decision\\.respond))$`);
 const request=async(event:IpcMainInvokeEvent,input:unknown)=>{
  const window=getWindow();if(event.sender!==window?.webContents||event.senderFrame!==window.webContents.mainFrame)throw new Error('Invalid sender');
  if(!input||typeof input!=='object')throw new Error('Invalid request');
  const {method,path,body,key,csrf}=input as Record<string,unknown>;
  if(typeof path!=='string'||path.length>4096||!['GET','POST','PUT'].includes(String(method))||typeof key!=='string'&&key!==undefined||typeof csrf!=='string'&&csrf!==undefined)throw new Error('Invalid request');
  const parsed=new URL(path,url.origin);if(parsed.origin!==url.origin||![...parsed.searchParams.keys()].every(k=>['cursor','after','limit'].includes(k))||!(method==='GET'?read:method==='PUT'?put:post).test(parsed.pathname))throw new Error('Unsupported request');
  const payload=body===undefined?undefined:JSON.stringify(body);if(payload&&payload.length>1_000_000)throw new Error('Request too large');
  const response=await fetch(parsed,{method:String(method),redirect:'error',signal:AbortSignal.timeout(15000),headers:{Origin:'lykeion://desktop','Content-Type':'application/json',...(cookie?{Cookie:cookie}:{}),...(key?{'Idempotency-Key':String(key)}:{}),...(csrf?{'X-CSRF-Token':String(csrf)}:{})},body:payload});
  const setCookie=response.headers.get('set-cookie');if(setCookie&&parsed.pathname==='/v1/auth/exchange'&&response.ok){const match=/lykeion_session=([^;]+)/.exec(setCookie);if(match){cookie='lykeion_session='+match[1];try{vault?.save(cookie);}catch{cookie='';throw new Error('Secure session storage failed; sign in again when storage is available.');}}}
  if(parsed.pathname==='/v1/auth/logout'&&response.ok||response.status===401){cookie='';close();vault?.clear();}
  const text=await response.text();if(text.length>2_000_000)throw new Error('Response too large');
  return {status:response.status,body:text?JSON.parse(text):undefined};
 };
 const realtime=async(event:IpcMainInvokeEvent,input:unknown)=>{
  authorize(event);if(!input||typeof input!=='object')throw new Error('Invalid stream request');
  const {action,workspace,after,id:requestedID}=input as Record<string,unknown>;
  if(action==='open'){
   if(typeof workspace!=='string'||!new RegExp('^'+id+'$').test(workspace)||typeof after!=='string'||after.length>2048||!cookie)throw new Error('Invalid subscription');
   close();streamID=randomUUID();const thisID=streamID;
   const address=new URL(`/v1/workspaces/${workspace}/realtime`,url.origin);address.protocol=url.protocol==='https:'?'wss:':'ws:';
   const ws=stream=new WebSocket(address,'lykeion.workspace.v1',{headers:{Cookie:cookie,Origin:'lykeion://desktop'},followRedirects:false,handshakeTimeout:10000,maxPayload:65536,perMessageDeflate:false});closed=false;
   ws.on('open',()=>{if(thisID!==streamID)return;ws.send(JSON.stringify({schema_version:'1',type:'subscribe',message_id:'msg_'+randomUUID(),payload:{channels:['workspace'],subject_refs:[],after_cursor:after}}));});
   ws.on('message',(data,binary)=>{if(thisID!==streamID)return;if(binary){close();return;}try{const frame=JSON.parse(data.toString());if(frame.type==='subscribed' && (input as Record<string,unknown>).presence===true){const beat=()=>{if(ws.readyState===WebSocket.OPEN)ws.send(JSON.stringify({schema_version:'1',type:'presence.update',message_id:'msg_'+randomUUID(),payload:{subject_ref:{kind:'workspace',id:workspace,revision:'1'},state:'viewing'}}));};beat();clearInterval(heartbeat);heartbeat=setInterval(beat,10000);}if(frames.length>=64){ws.terminate();closed=true;frames=[{schema_version:'1',type:'resync_required',message_id:'msg_'+randomUUID(),payload:{reason:'slow_consumer',snapshot_path:`/v1/workspaces/${workspace}/members`}}];}else frames.push(frame);}catch{close();}});
   ws.on('close',()=>{if(thisID===streamID)closed=true;});ws.on('error',()=>{if(thisID===streamID)closed=true;});
   return {id:streamID,frames:[],closed:false};
  }
  if(requestedID!==streamID)throw new Error('Stale stream');
  if(action==='close'){close();return {id:streamID,frames:[],closed:true};}
  if(action!=='read')throw new Error('Unsupported stream action');
  const batch=frames;frames=[];return {id:streamID,frames:batch,closed};
 };
 return {request,realtime,close};
}
