import {describe,it,expect} from 'vitest';
import {ref} from 'vue';
import {liveSelection} from '../apps/dashboard/src/services/live-selection';
describe('authorization-aware detail selection',()=>{
 it('updates reviewed detail and immediately drops revoked records without reopening on a later grant',()=>{
  const records=ref([{id:'programme',name:'Original',revision:'1'}]);
  const selected=liveSelection(()=>records.value);
  selected.value=records.value[0];
  records.value=[{id:'programme',name:'Revised',revision:'2'}];
  expect(selected.value?.revision).toBe('2');
  records.value=[];
  expect(selected.value).toBeUndefined();
  records.value=[{id:'programme',name:'Re-granted',revision:'3'}];
  expect(selected.value).toBeUndefined();
 });
});
