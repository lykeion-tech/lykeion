<script setup lang="ts">
import {onMounted,ref} from 'vue';
import {control,connectControl,logoutControl,showLocalDrafts,acceptInvitation} from '../../services/control';
const success=ref('');
const endpoint=ref(control.endpoint||'http://127.0.0.1:18431'),code=ref(''),error=ref(''),busy=ref(false),invitation=ref(''),proof=ref('');
const native=Boolean(window.lykeionDesktop);
onMounted(async()=>{if(window.lykeionDesktop)endpoint.value=await window.lykeionDesktop.controlOrigin();});
async function run(action:()=>Promise<unknown>){busy.value=true;error.value='';try{await action();}catch(e){error.value=(e as Error).message;}finally{busy.value=false;}}
async function connect(){const value=code.value;code.value='';await run(()=>connectControl(endpoint.value,value||undefined));}
async function accept(){const value=proof.value;proof.value='';await run(async()=>{await acceptInvitation(invitation.value.trim(),value.trim());success.value='Invitation accepted. The workspace is now available in the workspace menu.';invitation.value='';});}
</script>
<template>
<section class="control-connection">
 <h3>Research control plane</h3>
 <form @submit.prevent="connect">
  <div class="connection-box">
   <label><span>API endpoint<small v-if="native">Set by the desktop launcher.</small></span><input v-model="endpoint" type="url" :readonly="native" required aria-label="API endpoint"/></label>
   <label v-if="!control.session"><span>Single-use login code<small>Issued by your installation administrator.</small></span><input v-model="code" type="password" autocomplete="off" aria-label="Single-use login code"/></label>
   <div v-else class="identity"><span>{{control.session.display_name}}<small>Account ID: {{control.session.user_id}}</small></span><span>{{control.connection}}</span></div>
  </div>
  <p>Local drafts stay on this device. Connected workspaces load their own records.</p>
  <div class="actions"><button class="button primary" :disabled="busy">{{busy?'Connecting…':control.session?'Refresh connection':'Connect'}}</button><button v-if="control.session" type="button" class="button" :disabled="busy" @click="run(logoutControl)">Log out</button><button v-if="control.active" type="button" class="text-button" @click="showLocalDrafts">Open local drafts</button></div>
 </form>
 <form v-if="control.session" class="accept-invitation" @submit.prevent="accept">
  <h3>Accept an invitation</h3><p>Invitations are bound to your account. Accepting joins the workspace with the role chosen by its administrator.</p>
  <div class="connection-box"><label><span>Invitation ID</span><input v-model="invitation" required aria-label="Invitation ID"/></label><label><span>Invitation proof</span><input v-model="proof" required type="password" autocomplete="off" aria-label="Invitation proof"/></label></div>
  <button class="button primary" :disabled="busy">Accept invitation</button>
 </form>
 <p v-if="success" role="status">{{success}}</p>
 <p v-if="error" class="form-error" role="alert">{{error}}</p>
</section>
</template>
<style scoped>
h3{font-size:14px;font-weight:600;margin:0 2px 12px}.connection-box{border:1px solid var(--border);border-radius:12px;background:var(--input);overflow:hidden}.connection-box label,.identity{display:flex;align-items:center;justify-content:space-between;gap:20px;min-height:64px;padding:14px 16px;font-size:14px}.connection-box label+label,.identity{border-top:1px solid var(--border)}small{display:block;font-size:12px;color:var(--muted);margin-top:4px;overflow-wrap:anywhere}input{width:47%;min-width:0;border:1px solid var(--border);border-radius:7px;padding:6px 10px;background:transparent;font:inherit;height:32px}p{font-size:13px;line-height:20px;color:var(--muted);margin:16px 0}.actions{display:flex;align-items:center;gap:12px}.accept-invitation{margin-top:32px}.accept-invitation>.button{margin-top:16px}
</style>
