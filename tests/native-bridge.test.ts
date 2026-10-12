import {afterEach,describe,it,expect,vi} from 'vitest';
import {controlBridge} from '../apps/desktop/src/control-bridge';
import type {BrowserWindow,IpcMainInvokeEvent} from 'electron';
function fixture(){const frame={};const contents={mainFrame:frame};const window={webContents:contents} as unknown as BrowserWindow;const event={sender:contents,senderFrame:frame} as unknown as IpcMainInvokeEvent;return {event,bridge:controlBridge(()=>window,'http://127.0.0.1:18431')}}
afterEach(()=>vi.unstubAllGlobals());
describe('native credential boundary',()=>{
 it('keeps the cookie in the host and rejects a renderer-selected destination',async()=>{
  const requests:RequestInit[]=[];vi.stubGlobal('fetch',vi.fn(async(_url,init)=>{requests.push(init);return new Response(JSON.stringify({data:{user_id:'usr_test'}}),{status:200,headers:{'set-cookie':'lykeion_session=private-test-token; HttpOnly'}});}));
  const {event,bridge}=fixture();const result=await bridge.request(event,{method:'POST',path:'/v1/auth/exchange',body:{code:'single-use'}});
  expect(JSON.stringify(result)).not.toContain('private-test-token');
  await bridge.request(event,{method:'GET',path:'/v1/session'});
  expect(requests[1]?.headers).toMatchObject({Cookie:'lykeion_session=private-test-token',Origin:'lykeion://desktop'});
  await expect(bridge.request(event,{method:'GET',path:'https://untrusted.test/v1/session'})).rejects.toThrow('Unsupported');
  await expect(bridge.request(event,{method:'GET',path:'/v1/session?url=https://untrusted.test'})).rejects.toThrow('Unsupported');
  bridge.close();
 });
 it('rejects subframes and unknown commands',async()=>{
  const {event,bridge}=fixture();
  await expect(bridge.request({...event,senderFrame:{}} as IpcMainInvokeEvent,{method:'GET',path:'/v1/session'})).rejects.toThrow('Invalid sender');
  await expect(bridge.request(event,{method:'POST',path:'/v1/workspaces/wsp_test/commands/gate.force'})).rejects.toThrow('Unsupported');
  bridge.close();
 });
});
