import {readFileSync,writeFileSync,renameSync,unlinkSync,mkdirSync} from 'node:fs';
import {dirname} from 'node:path';
import {randomUUID} from 'node:crypto';
export interface SessionVault {load():string;save(cookie:string):void;clear():void}
export interface Encryptor {available():boolean;encrypt(value:string):Buffer;decrypt(value:Buffer):string}
// The origin is inside the encrypted envelope: copying a file cannot redirect
// its credential to a different service. No plaintext fallback is permitted.
export function encryptedSessionVault(file:string,origin:string,crypto:Encryptor):SessionVault {
 return {
  load(){if(!crypto.available())return '';try{const data=readFileSync(file);if(data.length>16384)return '';const value=JSON.parse(crypto.decrypt(data));return value.origin===origin&&typeof value.cookie==='string'&&/^lykeion_session=[A-Za-z0-9_-]+$/.test(value.cookie)?value.cookie:'';}catch{return '';}},
  save(cookie){if(!crypto.available())throw new Error('Secure credential storage unavailable');mkdirSync(dirname(file),{recursive:true,mode:0o700});const temporary=file+'.'+randomUUID();try{writeFileSync(temporary,crypto.encrypt(JSON.stringify({origin,cookie})),{mode:0o600,flag:'wx'});renameSync(temporary,file);}finally{try{unlinkSync(temporary);}catch{}}},
  clear(){try{unlinkSync(file);}catch(error){if((error as NodeJS.ErrnoException).code!=='ENOENT')throw error;}}
 };
}
