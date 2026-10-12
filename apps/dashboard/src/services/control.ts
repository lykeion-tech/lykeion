import { reactive, computed } from 'vue';
import { ControlClient, ControlError, browserTransport, normalizeEndpoint, type Model, type Page, type Read } from '../../../../packages/api-client/src/control';
export const control=reactive({active:false,loading:false,error:'',endpoint:'',session:null as Model<'Session'>|null,workspaces:[] as Model<'Workspace'>[],workspace:null as Model<'Workspace'>|null,members:[] as Model<'Member'>[],invitations:[] as Model<'Invitation'>[],decisions:[] as Model<'MembershipDecision'>[],commands:[] as string[],connection:'Disconnected'});
export const currentMember=computed(()=>control.members.find(m=>m.user_id===control.session?.user_id));
export const canManageMembers=computed(()=>['owner','admin'].includes(currentMember.value?.access_role??''));
let nativePoll:ReturnType<typeof setTimeout>|undefined;let nativeStream='';let nativeLive=false;
let client:ControlClient|undefined;let generation=0;let poll:ReturnType<typeof setTimeout>|undefined;let socket:WebSocket|undefined;let cursor='';
function stop(){clearTimeout(nativePoll);nativeLive=false;if(nativeStream){void window.lykeionDesktop?.realtime({action:'close',id:nativeStream}).catch(()=>{});nativeStream='';}clearTimeout(poll);socket?.close();socket=undefined;}
function clearWorkspace(){control.workspace=null;control.members=[];control.invitations=[];control.decisions=[];cursor='';}
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
  stop();generation++;client=next;control.active=true;control.endpoint=origin;control.session=session;control.commands=capabilities.data.supported_commands;control.workspaces=spaces.items;clearWorkspace();
  if(spaces.items[0])await selectWorkspace(spaces.items[0].id);
  control.connection='Connected';
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
 const decisions=await client!.all<'MembershipDecision'>(`/v1/workspaces/${selected.id}/membership-decisions`);
 if(run!==generation)return;
 control.decisions=decisions.items;
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
 let replaying=false;
 const schedule=()=>{clearTimeout(poll);if(run===generation&&control.workspace&&control.session)poll=setTimeout(()=>void replay(),2000);};
 const replay=async()=>{
  if(run!==generation||!control.workspace||replaying)return;
  replaying=true;
  try{const result=await client!.call<Page<Model<'Event'>>>('GET',`/v1/workspaces/${workspace.id}/events?after=${encodeURIComponent(cursor)}`);if(run!==generation)return;
   if(result.items.length)await refreshControl();else cursor=result.event_cursor;
   control.connection=(nativeLive||socket?.readyState===WebSocket.OPEN)?'Live':'Connected · polling';control.error='';
  }catch(e){if(run!==generation)return;if(e instanceof ControlError&&e.status===409){await refreshControl().catch(()=>{});}else{expired(e);control.connection='Reconnecting';control.error=(e as Error).message;}}
  replaying=false;schedule();
 };
 if(window.lykeionDesktop){
  const bridge=window.lykeionDesktop;
  void bridge.realtime({action:'open',workspace:workspace.id,after:cursor}).then(opened=>{
   if(run!==generation){void bridge.realtime({action:'close',id:opened.id}).catch(()=>{});return;}
   nativeStream=opened.id;
   const receive=async()=>{if(run!==generation)return;try{const batch=await bridge.realtime({action:'read',id:opened.id});if(run!==generation)return;
    nativeLive=!batch.closed;if(batch.frames.length)void replay();
    if(batch.closed){control.connection='Connected · polling';return;}
    nativePoll=setTimeout(()=>void receive(),250);
   }catch{nativeLive=false;}};
   void receive();
  }).catch(()=>{if(run===generation)control.connection='Connected · polling';});
 }else{
  const url=new URL(control.endpoint);url.protocol=url.protocol==='https:'?'wss:':'ws:';url.pathname=`/v1/workspaces/${workspace.id}/realtime`;
  socket=new WebSocket(url,'lykeion.workspace.v1');
  socket.onopen=()=>{if(run!==generation)return;socket?.send(JSON.stringify({schema_version:'1',type:'subscribe',message_id:'msg_'+crypto.randomUUID(),payload:{channels:['workspace'],subject_refs:[],after_cursor:cursor}}));control.connection='Live';};
  socket.onmessage=()=>{if(run===generation)void replay();};
  socket.onclose=()=>{if(run===generation)control.connection='Connected · polling';};
 }
 schedule();
}
export async function controlCommand(name:string,payload:unknown,versions:Model<'Ref'>[]=[],key:string=crypto.randomUUID()){
 if(!client||!control.workspace||!control.commands.includes(name))throw new Error('This action is not supported by the connected service.');
 const op=await client.command(control.workspace,name,payload,versions,key);
 if(op.state!=='succeeded')throw new Error(`Operation ${op.id}: ${op.state}`);
 await refreshControl();return op;
}
export async function invitationProof(id:string){if(!client||!control.workspace)throw new Error('Not connected');return (await client.call<Read<Model<'InvitationProof'>>>('GET',`/v1/workspaces/${control.workspace.id}/invitations/${id}/proof`)).data.proof;}
export async function acceptInvitation(id:string,proof:string){if(!client)throw new Error('Sign in first');await client.call('POST',`/v1/invitations/${id}/accept`,{proof,revision:'1'},crypto.randomUUID());await refreshControl();}
export async function logoutControl(){if(client)await client.call('POST','/v1/auth/logout');stop();generation++;client=undefined;clearWorkspace();control.session=null;control.workspaces=[];control.commands=[];control.active=false;control.connection='Disconnected';}
export function showLocalDrafts(){stop();generation++;client=undefined;clearWorkspace();control.session=null;control.workspaces=[];control.active=false;}
