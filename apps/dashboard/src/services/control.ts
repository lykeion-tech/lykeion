import { reactive, computed } from 'vue';
import { ControlClient, ControlError, browserTransport, normalizeEndpoint, type Model, type Page, type Read } from '../../../../packages/api-client/src/control';
export const control=reactive({active:false,loading:false,error:'',endpoint:'',session:null as Model<'Session'>|null,workspaces:[] as Model<'Workspace'>[],workspace:null as Model<'Workspace'>|null,members:[] as Model<'Member'>[],invitations:[] as Model<'Invitation'>[],decisions:[] as Model<'MembershipDecision'>[],programmes:[] as Model<'Programme'>[],grants:[] as Model<'AccessGrant'>[],commands:[] as string[],features:[] as string[],online:[] as string[],connection:'Disconnected'});
export const currentMember=computed(()=>control.members.find(m=>m.user_id===control.session?.user_id));
export const canManageMembers=computed(()=>['owner','admin'].includes(currentMember.value?.access_role??''));
let heartbeat:ReturnType<typeof setInterval>|undefined;
let nativePoll:ReturnType<typeof setTimeout>|undefined;let nativeStream='';let nativeLive=false;
let client:ControlClient|undefined;let generation=0;let poll:ReturnType<typeof setTimeout>|undefined;let socket:WebSocket|undefined;let cursor='';
function stop(){clearInterval(heartbeat);clearTimeout(nativePoll);nativeLive=false;if(nativeStream){void window.lykeionDesktop?.realtime({action:'close',id:nativeStream}).catch(()=>{});nativeStream='';}clearTimeout(poll);socket?.close();socket=undefined;}
function clearWorkspace(){control.online=[];control.workspace=null;control.members=[];control.invitations=[];control.decisions=[];control.programmes=[];control.grants=[];cursor='';}
function expired(error:unknown){if(error instanceof ControlError&&error.status===401){stop();clearWorkspace();control.workspaces=[];control.session=null;control.connection='Sign-in required';}else if(error instanceof ControlError&&error.status===404){clearWorkspace();}}
export async function connectControl(endpoint:string,code?:string){
 const origin=normalizeEndpoint(endpoint);
 const next=new ControlClient(window.lykeionDesktop?window.lykeionDesktop.control:browserTransport(origin));
 if(window.lykeionDesktop&&origin!==normalizeEndpoint(await window.lykeionDesktop.controlOrigin()))throw new Error('The desktop API destination is configured by its launcher.');
 control.loading=true;control.error='';
 try{
  const session=await next.session(code);
  const capabilities=await next.call<Read<Model<'Capabilities'>>>('GET','/v1/capabilities');
  const spaces=await next.all<'Workspace'>('/v1/workspaces');
  stop();generation++;client=next;control.active=true;control.endpoint=origin;control.session=session;control.commands=capabilities.data.supported_commands;control.features=capabilities.data.supported_features;control.workspaces=spaces.items;clearWorkspace();
  if(spaces.items[0])await selectWorkspace(spaces.items[0].id);
  control.connection='Connecting';try{localStorage.setItem('lykeion.control-origin',origin);}catch{}
 }catch(e){expired(e);throw e;}finally{control.loading=false;}
}
export async function selectWorkspace(id:string){
 if(!client)return;const selected=control.workspaces.find(w=>w.id===id);if(!selected)return;
 const run=++generation;stop();clearWorkspace();control.loading=true;control.error='';
 try{await loadWorkspace(selected,run);if(run===generation)startUpdates(run);}catch(e){if(run===generation){expired(e);control.error=(e as Error).message;}}finally{if(run===generation)control.loading=false;}
}
async function loadWorkspace(selected:Model<'Workspace'>,run:number){
 const members=await client!.all<'Member'>(`/v1/workspaces/${selected.id}/members`);
 const own=members.items.find(m=>m.user_id===control.session?.user_id);
 const invitations=['owner','admin'].includes(own?.access_role??'')?await client!.all<'Invitation'>(`/v1/workspaces/${selected.id}/invitations`):null;
 const programmes=await client!.all<'Programme'>(`/v1/workspaces/${selected.id}/programmes`);
 const grants=control.commands.includes('access.change')?await client!.all<'AccessGrant'>(`/v1/workspaces/${selected.id}/access-grants`):null;
 const decisions=await client!.all<'MembershipDecision'>(`/v1/workspaces/${selected.id}/membership-decisions`);
 if(run!==generation)return;
 control.grants=grants?.items??[];control.decisions=decisions.items;control.programmes=programmes.items;
 control.workspace={...selected,revision:members.snapshot_revision};control.members=members.items;control.invitations=invitations?.items??[];
 // Keep the earlier cursor: a mutation between reads must still cause a refresh.
 cursor=members.event_cursor;
}
export async function refreshControl(){
 if(!client)return;const run=generation;
 try{const spaces=await client.all<'Workspace'>('/v1/workspaces');if(run!==generation)return;control.workspaces=spaces.items;
 const selected=spaces.items.find(w=>w.id===control.workspace?.id);if(selected)await loadWorkspace(selected,run);else{clearWorkspace();if(spaces.items[0])await selectWorkspace(spaces.items[0].id);}
 }catch(e){if(run===generation){expired(e);control.error=(e as Error).message;}throw e;}
}
function startUpdates(run:number){
 const workspace=control.workspace;if(!workspace||!client)return;
 let replaying=false,opening=false,retryAt=0,failures=0;
 const current=()=>run===generation&&control.workspace?.id===workspace.id&&!!control.session;
 const lost=()=>{if(!current())return;nativeLive=false;control.online=[];opening=false;retryAt=Date.now()+Math.min(30000,1000*2**Math.min(failures++,5));control.connection='Connected · polling';};
 const schedule=()=>{clearTimeout(poll);if(current())poll=setTimeout(()=>void replay(),2000);};
 const onFrame=(value:unknown)=>{
  if(!current()||!value||typeof value!=='object')return;
  const f=value as {type?:string;payload?:{participants?:{user_id:string}[]}};
  if(f.type==='subscribed'){failures=0;nativeLive=true;control.connection='Live';}
  else if(f.type==='presence.snapshot'){control.online=[...new Set((f.payload?.participants??[]).map(p=>p.user_id))];}
  else if(f.type==='resync_required'){socket?.close();if(nativeStream)void window.lykeionDesktop?.realtime({action:'close',id:nativeStream}).catch(()=>{});lost();void replay();}
  else if(f.type==='event')void replay();
 };
 const open=()=>{
  if(!current()||opening||nativeLive||Date.now()<retryAt)return;opening=true;
  if(window.lykeionDesktop){
   const bridge=window.lykeionDesktop;
   void bridge.realtime({action:'open',workspace:workspace.id,after:cursor,presence:control.features.includes('workspace.presence')}).then(opened=>{
    if(!current()){void bridge.realtime({action:'close',id:opened.id}).catch(()=>{});return;}
    nativeStream=opened.id;
    const receive=async()=>{if(!current())return;try{const batch=await bridge.realtime({action:'read',id:opened.id});if(!current())return;
     batch.frames.forEach(onFrame);if(batch.closed){lost();return;}nativePoll=setTimeout(()=>void receive(),250);
    }catch{lost();}};void receive();
   }).catch(lost);
  }else{
   const url=new URL(control.endpoint);url.protocol=url.protocol==='https:'?'wss:':'ws:';url.pathname=`/v1/workspaces/${workspace.id}/realtime`;
   const ws=socket=new WebSocket(url,'lykeion.workspace.v1');
   const send=(type:string,payload:unknown)=>{if(current()&&ws.readyState===WebSocket.OPEN)ws.send(JSON.stringify({schema_version:'1',type,message_id:'msg_'+crypto.randomUUID(),payload}));};
   ws.onopen=()=>send('subscribe',{channels:['workspace'],subject_refs:[],after_cursor:cursor});
   ws.onmessage=event=>{if(!current())return;try{const frame=JSON.parse(event.data);onFrame(frame);if(frame.type==='subscribed'&&control.features.includes('workspace.presence')){const beat=()=>send('presence.update',{subject_ref:{kind:'workspace',id:workspace.id,revision:'1'},state:'viewing'});beat();clearInterval(heartbeat);heartbeat=setInterval(beat,10000);}}catch{ws.close();}};
   ws.onclose=()=>{if(!current()||socket!==ws)return;clearInterval(heartbeat);lost();};ws.onerror=()=>ws.close();
  }
 };
 const replay=async()=>{
  if(!current()||replaying)return;replaying=true;
  try{const result=await client!.call<Page<Model<'Event'>>>('GET',`/v1/workspaces/${workspace.id}/events?after=${encodeURIComponent(cursor)}`);if(!current())return;
   if(result.items.length)await refreshControl();else cursor=result.event_cursor;
   if(current()){control.connection=nativeLive?'Live':'Connected · polling';control.error='';open();}
  }catch(e){if(!current())return;if(e instanceof ControlError&&e.status===409){await refreshControl().catch(()=>{});open();}else{expired(e);if(current()){control.connection='Reconnecting';control.error=(e as Error).message;}}}
  finally{replaying=false;schedule();}
 };
 open();schedule();
}
export async function controlCommand(name:string,payload:unknown,versions:Model<'Ref'>[]=[],key:string=crypto.randomUUID()){
 if(!client||!control.workspace||!control.commands.includes(name))throw new Error('This action is not supported by the connected service.');
 const op=await client.command(control.workspace,name,payload,versions,key);
 if(op.state!=='succeeded')throw new Error(`Operation ${op.id}: ${op.state}`);
 await refreshControl();return op;
}
export async function invitationProof(id:string){if(!client||!control.workspace)throw new Error('Not connected');return (await client.call<Read<Model<'InvitationProof'>>>('GET',`/v1/workspaces/${control.workspace.id}/invitations/${id}/proof`)).data.proof;}
export async function acceptInvitation(id:string,proof:string){if(!client)throw new Error('Sign in first');await client.call('POST',`/v1/invitations/${id}/accept`,{proof,revision:'1'},crypto.randomUUID());await refreshControl();}
export async function logoutControl(){if(client)await client.call('POST','/v1/auth/logout');stop();generation++;client=undefined;clearWorkspace();control.session=null;control.workspaces=[];control.commands=[];control.active=false;control.connection='Disconnected';try{localStorage.removeItem('lykeion.control-origin');}catch{}}
export function showLocalDrafts(){try{localStorage.removeItem('lykeion.control-origin');}catch{}stop();generation++;client=undefined;clearWorkspace();control.session=null;control.workspaces=[];control.active=false;}

export async function submitConfiguration(kind:'domain-profile'|'gate-policy'|'grant-changes',content:string){
 if(!client||!control.workspace)throw new Error('Connect first.');
 const bytes=new TextEncoder().encode(content);if(bytes.length>65536)throw new Error('Configuration exceeds 64 KiB.');JSON.parse(content);
 const hash=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes))).map(b=>b.toString(16).padStart(2,'0')).join('');
 const base=`/v1/workspaces/${control.workspace.id}`;const key=crypto.randomUUID();
 const upload=await client.call<Read<Model<'UploadReservation'>>>('POST',base+'/uploads',{sha256:hash,byte_length:bytes.length},key);
 await client.call('PUT',base+`/uploads/${upload.data.id}/parts/1`,{content_base64:btoa(Array.from(bytes,b=>String.fromCharCode(b)).join(''))},key);
 const receipt=(await client.call<Read<Model<'Ref'>>>('POST',base+`/uploads/${upload.data.id}/complete`,{},key)).data;
 const operation=await controlCommand('artifact.submit',{upload_receipt_refs:[receipt],manifest_candidate:{files:[{path:'configuration.json',upload_ref:receipt}]},authored_kind:kind});
 return operation.result_refs[0]!;
}

export async function restoreControl(){
 let saved='';try{saved=localStorage.getItem('lykeion.control-origin')??'';}catch{}
 if(!saved)return;
 control.active=true;control.loading=true;
 try{await connectControl(saved);}catch(e){control.error=(e as Error).message;control.connection='Sign-in required';}finally{control.loading=false;}
}
