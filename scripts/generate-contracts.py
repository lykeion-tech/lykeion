#!/usr/bin/env python3
"""Owned wire schemas for frozen slices. Unfrozen catalogue entries are not endpoints."""
from pathlib import Path
import json
ROOT=Path(__file__).resolve().parents[1]
def obj(props,required=None):return {'type':'object','properties':props,'required':list(props) if required is None else required,'additionalProperties':False}
def ref(name):return {'$ref':f'#/components/schemas/{name}'}
def arr(items):return {'type':'array','items':items,'maxItems':1000}
def enum(*vals):return {'type':'string','enum':list(vals)}
def string(maximum=10000):return {'type':'string','minLength':1,'maxLength':maximum}
rev={'type':'string','pattern':'^(0|[1-9][0-9]*)$','maxLength':20}
s={
 'ID':{'type':'string','pattern':'^[a-z][a-z0-9_-]{2,127}$'},'Revision':rev,
 'Hash':{'type':'string','pattern':'^[a-f0-9]{64}$'},
 'Ref':obj({'kind':string(64),'id':ref('ID'),'revision':ref('Revision'),'sha256':ref('Hash')},['kind','id','revision']),
 'Target':obj({'kind':enum('workspace','programme','inquiry','request','document','membership','invitation'),'id':ref('ID')}),
 'Problem':obj({'schema_version':{'const':'1'},'code':string(64),'message':string(),'request_id':string(128),'retryable':{'type':'boolean'},'violations':arr(string()),'current_versions':arr(ref('Ref'))}),
 'Operation':obj({'schema_version':{'const':'1'},'id':ref('ID'),'command_name':string(100),'state':enum('queued','running','succeeded','blocked','failed','superseded'),'target_ref':ref('Target'),'accepted_at':{'type':'string','format':'date-time'},'result_refs':arr(ref('Ref')),'decision_ref':ref('Ref'),'failure':ref('Problem'),'event_cursor':string(2048)},['schema_version','id','command_name','state','target_ref','accepted_at','result_refs','event_cursor']),
 'Member':obj({'id':ref('ID'),'user_id':ref('ID'),'workspace_id':ref('ID'),'display_name':string(120),'access_role':enum('owner','admin','member','viewer'),'state':enum('active','removed'),'revision':rev}),
 'Workspace':obj({'id':ref('ID'),'name':string(120),'revision':rev}),
 'Programme':obj({'id':ref('ID'),'workspace_id':ref('ID'),'name':string(120),'objective':string(),'revision':rev,'profile_ref':ref('Ref'),'policy_refs':arr(ref('Ref')),'budget_ceiling':ref('BudgetCeiling')}),
 'BudgetCeiling':obj({'currency':{'type':'string','pattern':'^[A-Z]{3}$'},'model_microunits':rev,'cpu_milliseconds':rev,'gpu_milliseconds':rev,'elapsed_milliseconds':rev}),
 'Session':obj({'user_id':ref('ID'),'display_name':string(120),'csrf_token':string(256),'expires_at':{'type':'string','format':'date-time'}}),
 'Capabilities':obj({'api_version':{'const':'1'},'workspace_protocol':{'const':'lykeion.workspace.v1'},'worker_protocol':{'const':'lykeion.worker.v1'},'supported_commands':arr(string(100)),'supported_features':arr(string(100))}),
 'Attribution':obj({'initiator_id':ref('ID'),'authorizer_id':ref('ID'),'actor_id':ref('ID'),'actor_kind':enum('human','service','agent'),'delegation_refs':arr(ref('Ref'))}),
 'Event':obj({'id':string(128),'type':string(100),'subject_ref':ref('Ref'),'revision':rev,'recorded_at':{'type':'string','format':'date-time'},'operation_id':ref('ID'),'causation_id':string(128),'correlation_id':string(128),'attribution_ref':ref('Ref'),'payload':obj({'changed_fields':arr(string(100))})}),
 'Invitation':obj({'id':ref('ID'),'workspace_id':ref('ID'),'recipient':string(320),'requested_role':enum('admin','member','viewer'),'state':enum('pending','accepted','revoked','expired'),'expires_at':{'type':'string','format':'date-time'},'revision':rev}),
}
s['MembershipDecision']=obj({'id':ref('ID'),'workspace_id':ref('ID'),'requester_id':ref('ID'),'responder_id':ref('ID'),'member_id':ref('ID'),'action':enum('change-access','transfer-owner'),'requested_role':enum('owner','admin','member','viewer'),'state':enum('pending','approved','rejected','expired'),'revision':rev,'expires_at':{'type':'string','format':'date-time'},'subject_versions':arr(ref('Ref'))})
s['AccessGrant']=obj({'programme_id':ref('ID'),'user_id':ref('ID'),'capabilities':arr(enum('read','comment','edit','manage-access')),'revision':rev})
s['DomainProfile']=obj({'name':string(120),'domain':{'const':'computational'},'description':string()})
s['GatePolicyRule']=obj({'id':string(128),'require':{'type':'array','minItems':1,'maxItems':1000,'items':string(128)},'outcome':enum('advance','block','route','escalate'),'destination':enum('Backlog','Grounding','Hypotheses','Experimentation','Validation','Reporting','Done')},['id','require','outcome'])
s['GatePolicy']=obj({'version':string(128),'required':{'type':'array','minItems':1,'maxItems':1000,'uniqueItems':True,'items':string(128)},'rules':{'type':'array','minItems':1,'maxItems':1000,'items':ref('GatePolicyRule')}})
s['GrantPlan']=obj({'subject_ref':ref('Ref'),'changes':{'type':'array','minItems':1,'maxItems':100,'items':obj({'user_id':ref('ID'),'capabilities':{'type':'array','maxItems':4,'uniqueItems':True,'items':enum('read','comment','edit','manage-access')}})}})
s['ConfigManifest']=obj({'files':{'type':'array','minItems':1,'maxItems':1,'items':obj({'path':{'const':'configuration.json'},'upload_ref':ref('Ref')})}})
s['UploadReservation']=obj({'id':ref('ID'),'maximum_bytes':{'const':65536},'part_count':{'const':1}})
s['UploadPartReceipt']=obj({'upload_id':ref('ID'),'sha256':ref('Hash')})
s['ArtifactContent']=obj({'artifact_ref':ref('Ref'),'authored_kind':enum('domain-profile','gate-policy','grant-changes'),'content_base64':string(90000)})
s['ArtifactRecord']=obj({'id':ref('ID'),'revision':rev,'sha256':ref('Hash'),'manifest':ref('ConfigManifest')})
# Write payloads are closed and intentionally cover only the S01 freeze.
payloads={
 'artifact.submit':obj({'upload_receipt_refs':arr(ref('Ref')),'manifest_candidate':ref('ConfigManifest'),'authored_kind':enum('domain-profile','gate-policy','grant-changes')}),
 'access.change':obj({'subject_ref':ref('Ref'),'grant_changes_ref':ref('Ref')}),
 'programme.create':obj({'name':string(120),'objective':string(),'profile_ref':ref('Ref'),'initial_policy_refs':arr(ref('Ref')),'budget_ceiling':ref('BudgetCeiling')}),
 'programme.amend-objective':obj({'objective_ref':ref('Ref'),'reason':string()}),
 'member.invite':obj({'destination':ref('ID'),'requested_role':enum('admin','member','viewer'),'expires_at':{'type':'string','format':'date-time'}}),
 'invitation.revoke':obj({'invitation_id':ref('ID'),'reason':string()}),
 'member.remove':obj({'member_id':ref('ID'),'ownership_handoff_ref':ref('Ref'),'reason':string()},['member_id','reason']),
 'member.leave':obj({'ownership_handoff_ref':ref('Ref')},[]),
 'member.change-access':obj({'member_id':ref('ID'),'requested_access':enum('admin','member','viewer'),'approved_decision_ref':ref('Ref')}),
 'workspace.transfer-owner':obj({'recipient_member_id':ref('ID'),'acceptance_decision_ref':ref('Ref')}),
 'member.request-access-change':obj({'member_id':ref('ID'),'requested_access':enum('admin','member','viewer'),'expires_at':{'type':'string','format':'date-time'}}),
 'workspace.request-owner-transfer':obj({'recipient_member_id':ref('ID'),'expires_at':{'type':'string','format':'date-time'}}),
 'decision.respond':obj({'decision_id':ref('ID'),'decision_revision':rev,'choice_id':enum('approve','reject'),'subject_versions':arr(ref('Ref'))}),
 'preference.update':obj({'preference_kind':enum('theme','sidebar','pane_width'),'value':{'oneOf':[enum('system','light','dark'),{'type':'boolean'},{'type':'integer','minimum':240,'maximum':960}]}}),
}
paths={}
def response(schema):return {'description':'Typed response','content':{'application/json':{'schema':schema}}}
def read_schema(name):return obj({'schema_version':{'const':'1'},'data':ref(name),'event_cursor':string(2048)})
def page_schema(name):return obj({'schema_version':{'const':'1'},'items':arr(ref(name)),'next_cursor':string(2048),'snapshot_revision':rev,'event_cursor':string(2048)},['schema_version','items','snapshot_revision','event_cursor'])
workspace_param={'name':'workspace_id','in':'path','required':True,'schema':ref('ID')}
for name,payload in payloads.items():
 key=''.join(x.title() for x in name.replace('-','.').split('.'))+'Command'
 s[key]=obj({'schema_version':{'const':'1'},'target':ref('Target'),'expected_versions':arr(ref('Ref')),'payload':payload})
 paths['/v1/workspaces/{workspace_id}/commands/'+name]={'post':{'operationId':key,'parameters':[workspace_param,{'name':'Idempotency-Key','in':'header','required':True,'schema':string(128)},{'name':'X-CSRF-Token','in':'header','required':False,'description':'Required for cookie authentication; CLI bearer sessions do not use CSRF tokens.','schema':string(256)}],'requestBody':{'required':True,'content':{'application/json':{'schema':ref(key)}}},'responses':{'202':response(ref('Operation')),'default':response(ref('Problem'))}}}
for path,model,page in [('/v1/session','Session',False),('/v1/capabilities','Capabilities',False),('/v1/workspaces','Workspace',True),('/v1/workspaces/{workspace_id}/members','Member',True),('/v1/workspaces/{workspace_id}/programmes','Programme',True),('/v1/workspaces/{workspace_id}/access-grants','AccessGrant',True),('/v1/workspaces/{workspace_id}/invitations','Invitation',True),('/v1/workspaces/{workspace_id}/membership-decisions','MembershipDecision',True),('/v1/workspaces/{workspace_id}/events','Event',True)]:
 params=[workspace_param] if '{workspace_id}' in path else []
 if page:params += [{'name':'cursor','in':'query','schema':string(2048)},{'name':'limit','in':'query','schema':{'type':'integer','minimum':1,'maximum':100}}]
 if path.endswith('/events'):params.append({'name':'after','in':'query','schema':string(2048)})
 paths[path]={'get':{'operationId':'get'+model+('Page' if page else ''),'parameters':params,'responses':{'200':response(page_schema(model) if page else read_schema(model)),'default':response(ref('Problem'))}}}
paths['/v1/workspaces/{workspace_id}/operations/{operation_id}']={'get':{'operationId':'getOperation','parameters':[workspace_param,{'name':'operation_id','in':'path','required':True,'schema':ref('ID')}],'responses':{'200':response(ref('Operation')),'default':response(ref('Problem'))}}}
paths['/v1/auth/exchange']={'post':{'operationId':'exchangeInstallationCode','security':[],'requestBody':{'required':True,'content':{'application/json':{'schema':obj({'code':string(256)})}}},'responses':{'200':response(read_schema('Session')),'default':response(ref('Problem'))}}}
s['DeviceSession']=obj({'credential':string(256),'session':ref('Session')})
paths['/v1/auth/device/exchange']={'post':{'operationId':'exchangeDeviceCode','security':[],'requestBody':{'required':True,'content':{'application/json':{'schema':obj({'code':string(256)})}}},'responses':{'200':response(read_schema('DeviceSession')),'default':response(ref('Problem'))}}}
paths['/v1/auth/logout']={'post':{'operationId':'logout','parameters':[{'name':'X-CSRF-Token','in':'header','required':False,'description':'Required for cookie authentication; CLI bearer sessions do not use CSRF tokens.','schema':string(256)}],'responses':{'204':{'description':'Session revoked'}}}}
s['InvitationProof']=obj({'invitation_id':ref('ID'),'proof':string(256)})
paths['/v1/workspaces/{workspace_id}/invitations/{invitation_id}/proof']={'get':{'operationId':'getInvitationProof','parameters':[workspace_param,{'name':'invitation_id','in':'path','required':True,'schema':ref('ID')}],'responses':{'200':response(read_schema('InvitationProof')),'default':response(ref('Problem'))}}}
paths['/v1/invitations/{invitation_id}/accept']={'post':{'operationId':'acceptInvitation','parameters':[{'name':'invitation_id','in':'path','required':True,'schema':ref('ID')},{'name':'Idempotency-Key','in':'header','required':True,'schema':string(128)},{'name':'X-CSRF-Token','in':'header','required':False,'description':'Required for cookie authentication; CLI bearer sessions do not use CSRF tokens.','schema':string(256)}],'requestBody':{'required':True,'content':{'application/json':{'schema':obj({'proof':string(256),'revision':rev})}}},'responses':{'202':response(ref('Operation')),'default':response(ref('Problem'))}}}
paths['/v1/me/operations/{operation_id}']={'get':{'operationId':'getOwnOperation','parameters':[{'name':'operation_id','in':'path','required':True,'schema':ref('ID')}],'responses':{'200':response(ref('Operation')),'default':response(ref('Problem'))}}}
paths['/v1/workspaces/{workspace_id}/programmes/{programme_id}']={'get':{'operationId':'getProgramme','parameters':[workspace_param,{'name':'programme_id','in':'path','required':True,'schema':ref('ID')}],'responses':{'200':response(read_schema('Programme')),'default':response(ref('Problem'))}}}
for suffix,method,body,model in [
 ('/uploads','post',obj({'sha256':ref('Hash'),'byte_length':{'type':'integer','minimum':1,'maximum':65536}}),'UploadReservation'),
 ('/uploads/{upload_id}/parts/1','put',obj({'content_base64':string(90000)}),'UploadPartReceipt'),
 ('/uploads/{upload_id}/complete','post',None,'Ref'),
 ('/artifacts/{artifact_id}','get',None,'ArtifactRecord'),
 ('/artifacts/{artifact_id}/content','get',None,'ArtifactContent')]:
 path='/v1/workspaces/{workspace_id}'+suffix
 params=[workspace_param]+[{'name':name,'in':'path','required':True,'schema':ref('ID')} for name in ['upload_id','artifact_id'] if '{'+name+'}' in path]
 operation={'operationId':method+model,'parameters':params,'responses':{'200':response(read_schema(model)),'default':response(ref('Problem'))}}
 if method!='get':params += [{'name':'Idempotency-Key','in':'header','required':True,'schema':string(128)},{'name':'X-CSRF-Token','in':'header','required':False,'schema':string(256)}]
 if body:operation['requestBody']={'required':True,'content':{'application/json':{'schema':body}}}
 paths[path]={method:operation}
control={'openapi':'3.1.0','info':{'title':'Lykeion control API — implemented slice contracts','version':'0.1.0'},'security':[{'session':[]},{'device':[]}],'paths':paths,'components':{'securitySchemes':{'session':{'type':'apiKey','in':'cookie','name':'lykeion_session'},'device':{'type':'http','scheme':'bearer','description':'Dedicated CLI session; browser tokens are not accepted.'}},'schemas':s}}
# Schemas for all workspace frame types; payloads are explicitly bounded.
frame_payloads={
 'subscribe':obj({'channels':arr(enum('workspace','programme','document','session')),'subject_refs':arr(ref('Ref')),'after_cursor':string(2048)},['channels','subject_refs']),
 'subscribed':obj({'subscription_id':ref('ID'),'high_water_cursor':string(2048),'authorization_epoch':rev}),
 'unsubscribe':obj({'subscription_id':ref('ID')}),
 'event':ref('Event'), 'ack':obj({'cursor':string(2048)}),
 'presence.update':obj({'subject_ref':ref('Ref'),'state':enum('viewing','editing','typing'),'selection':obj({'block_id':ref('ID'),'revision':rev,'anchor':{'type':'integer','minimum':0},'focus':{'type':'integer','minimum':0}})},['subject_ref','state']),
 'presence.snapshot':obj({'participants':arr(obj({'user_id':ref('ID'),'session_id':ref('ID'),'subject_ref':ref('Ref'),'state':enum('viewing','editing','typing'),'expires_at':{'type':'string','format':'date-time'}}))}),
 'presence.changed':obj({'user_id':ref('ID'),'session_id':ref('ID'),'state':enum('viewing','editing','typing','left'),'expires_at':{'type':'string','format':'date-time'}}),
 'document.sync':obj({'document_id':ref('ID'),'known_revision':rev}),
 'document.snapshot':obj({'document_id':ref('ID'),'revision':rev,'content_ref':ref('Ref'),'edit_log_cursor':string(2048)}),
 'document.changed':obj({'document_id':ref('ID'),'revision':rev,'operation_id':ref('ID'),'edit_ref':ref('Ref')}),
 'resync_required':obj({'reason':enum('cursor_expired','access_changed','slow_consumer','protocol_changed'),'snapshot_path':{'type':'string','pattern':'^/v1/workspaces/'}}),
 'error':ref('Problem')}
frames=[obj({'schema_version':{'const':'1'},'type':{'const':name},'message_id':ref('ID'),'subscription_id':ref('ID'),'cursor':string(2048),'payload':payload},['schema_version','type','message_id','payload']) for name,payload in frame_payloads.items()]
protocol={'$schema':'https://json-schema.org/draft/2020-12/schema','$id':'https://lykeion.local/schemas/workspace-frame-v1','oneOf':frames,'components':{'schemas':s}}
ROOT.joinpath('contracts/control.openapi.json').write_text(json.dumps(control,indent=2)+'\n')
ROOT.joinpath('contracts/realtime.protocol.json').write_text(json.dumps(protocol,indent=2)+'\n')
ROOT.joinpath('contracts/schemas/common.json').write_text(json.dumps({'$schema':'https://json-schema.org/draft/2020-12/schema','components':{'schemas':s}},indent=2)+'\n')
print(f'Generated {len(paths)} implemented route contracts, {len(s)} schemas and {len(frames)} frame variants; later slice endpoints remain unfrozen.')
