import {it,expect} from 'vitest';
import {mkdtempSync,readFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';import {join} from 'node:path';
import {createCipheriv,createDecipheriv,randomBytes} from 'node:crypto';
import {encryptedSessionVault} from '../apps/desktop/src/session-vault';
it('persists encrypted sessions, binds origin, clears logout and fails closed',()=>{
 const dir=mkdtempSync(join(tmpdir(),'lykeion-vault-')),file=join(dir,'session');
 const key=randomBytes(32),iv=randomBytes(12);
 const crypto={available:()=>true,encrypt:(v:string)=>{const c=createCipheriv('aes-256-gcm',key,iv);const data=Buffer.concat([c.update(v),c.final()]);return Buffer.concat([c.getAuthTag(),data]);},decrypt:(v:Buffer)=>{const c=createDecipheriv('aes-256-gcm',key,iv);c.setAuthTag(v.subarray(0,16));return Buffer.concat([c.update(v.subarray(16)),c.final()]).toString();}};
 try{
  const vault=encryptedSessionVault(file,'https://control.example',crypto);vault.save('lykeion_session=test_secret');
  expect(readFileSync(file).toString()).not.toContain('test_secret');
  expect(encryptedSessionVault(file,'https://control.example',crypto).load()).toBe('lykeion_session=test_secret');
  expect(encryptedSessionVault(file,'https://other.example',crypto).load()).toBe('');
  const unavailable=encryptedSessionVault(file,'https://control.example',{...crypto,available:()=>false});expect(unavailable.load()).toBe('');expect(()=>unavailable.save('secret')).toThrow('unavailable');
  vault.clear();expect(vault.load()).toBe('');
 }finally{rmSync(dir,{recursive:true,force:true});}
});
