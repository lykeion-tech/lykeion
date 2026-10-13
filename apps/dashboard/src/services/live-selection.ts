import {computed,ref,watch} from 'vue';

// Keep only identity locally; content and authority come from the latest projection.
export function liveSelection<T extends {id:string}>(items:()=>readonly T[]){
 const id=ref<string>();
 watch(items,current=>{if(id.value&&!current.some(item=>item.id===id.value))id.value=undefined;},{flush:'sync'});
 return computed<T|undefined>({get:()=>items().find(item=>item.id===id.value),set:item=>{id.value=item?.id;}});
}
