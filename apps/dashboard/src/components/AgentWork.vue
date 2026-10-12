<script setup lang="ts">
import {computed,ref} from 'vue';import {useRouter} from 'vue-router';import {state} from '../services/workspace';import Icon from './Icon.vue';
const props=defineProps<{agentId:string}>();const router=useRouter(),search=ref(''),mode=ref('Assigned'),filter=ref('all'),showEmpty=ref(true),menu=ref(''),collapsed=ref<string[]>([]),selectedIds=ref<string[]>([]);const stages=['Grounding','Hypotheses','Experimentation','Validation','Blocked','Reporting'];
const tasks=computed(()=>(state.data?.inquiries??[]).filter(x=>(mode.value==='Assigned'?x.assigneeId===props.agentId:x.creatorId===props.agentId)&&x.title.toLowerCase().includes(search.value.toLowerCase())&&(filter.value==='all'||x.priority===filter.value)));
const groups=computed(()=>stages.map(stage=>({stage,tasks:tasks.value.filter(x=>stage==='Blocked'?x.status==='blocked':x.status!=='blocked'&&x.stage===stage)})).filter(x=>showEmpty.value||x.tasks.length));
function selectGroup(ids:string[]){const all=ids.every(id=>selectedIds.value.includes(id));selectedIds.value=all?selectedIds.value.filter(id=>!ids.includes(id)):[...new Set([...selectedIds.value,...ids])]}
function toggle(stage:string){collapsed.value=collapsed.value.includes(stage)?collapsed.value.filter(x=>x!==stage):[...collapsed.value,stage]}
</script>
<template>
<div class="agent-work">
<div class="work-toolbar">
<label>
<Icon name="search" :size="14"/>
<input v-model="search" placeholder="Search tasks..." aria-label="Search agent tasks"/>
</label>
<button v-for="item in ['Assigned','Created']" :key="item" class="button compact" :class="{active:mode===item}" @click="mode=item">{{item}}</button>
<div class="menu-anchor push-right">
<button class="button compact" @click="menu=menu==='filter'?'':'filter'">
<Icon name="filter" :size="14"/>Filter</button>
<div v-if="menu==='filter'" class="floating-menu">
<small>Priority</small>
<button v-for="priority in ['all','urgent','high','medium','low','none']" @click="filter=priority;menu=''">{{priority}}<Icon v-if="filter===priority" name="check" class="push-right" :size="14"/>
</button>
</div>
</div>
<div class="menu-anchor">
<button class="button compact" @click="menu=menu==='display'?'':'display'">
<Icon name="filters" :size="14"/>Display</button>
<div v-if="menu==='display'" class="floating-menu">
<button @click="showEmpty=!showEmpty">
<Icon v-if="showEmpty" name="check" :size="14"/>Show empty groups</button>
</div>
</div>
</div>
<section v-for="group in groups" :key="group.stage">
<div class="work-group-heading"><input type="checkbox" :aria-label="'Select '+group.stage+' tasks'" :checked="group.tasks.length>0&&group.tasks.every(x=>selectedIds.includes(x.id))" :indeterminate="group.tasks.some(x=>selectedIds.includes(x.id))&&!group.tasks.every(x=>selectedIds.includes(x.id))" :disabled="!group.tasks.length" @change="selectGroup(group.tasks.map(x=>x.id))"/><button class="work-group-header" :aria-expanded="!collapsed.includes(group.stage)" @click="toggle(group.stage)">
<Icon :name="collapsed.includes(group.stage)?'right':'down'" :size="12"/>
<span :class="'work-stage-dot '+group.stage.toLowerCase()"/>{{group.stage}}<small>{{group.tasks.length}}</small>
</button></div>
<template v-if="!collapsed.includes(group.stage)">
<button v-for="task in group.tasks" :key="task.id" class="agent-work-row" @click="router.push('/tasks/'+task.id)">
<small>{{state.data?.workspace.taskPrefix}}-{{state.data!.inquiries.indexOf(task)+1}}</small>
<span>{{task.title}}</span>
<Icon name="bot" class="push-right" :size="14"/>
</button>
<p v-if="!group.tasks.length">No tasks</p>
</template>
</section>
<p v-if="!groups.length" class="agent-work-empty">No matching tasks</p>
</div></template>
<style scoped>
.agent-work{position:relative;left:-5px;width:calc(100% - 48px);max-width:1440px;margin:0 auto;overflow:auto;flex:1;padding:0 12px 16px}
.work-toolbar{display:flex;align-items:center;gap:4px;padding:8px 26px 8px 4px;border-bottom:1px solid var(--border);margin-bottom:4px}
.work-toolbar>label{display:flex;align-items:center;gap:8px;border:1px solid var(--border);border-radius:6px;padding:0 10px;height:32px;width:256px;margin-right:8px;color:var(--muted)}
.work-toolbar input{width:100%;min-width:0;border:0;background:none;outline:0;font-size:13px}
.work-group-heading{display:flex;align-items:center;gap:8px;background:var(--modal);height:40px;border-radius:8px;padding:0 12px}.work-group-heading>input{appearance:none;width:13px;height:13px;border:1px solid var(--muted);border-radius:3px;background:var(--surface);margin:0;flex:none}.work-group-heading>input:checked{background:var(--primary);box-shadow:inset 0 0 0 2px var(--surface)}.work-group-heading>input:indeterminate{background:var(--selected)}.work-group-heading>input:disabled{opacity:1}.work-group-header{display:flex;align-items:center;gap:8px;height:40px;border-radius:8px;flex:1;padding:0;font-size:12px;text-align:left;font-weight:500}
.work-group-header small{font-weight:400;color:var(--muted);font-size:12px}
.agent-work section>p{height:68px;display:grid;place-items:center;font-size:12px;color:var(--muted)}
.agent-work-row{display:flex;align-items:center;gap:28px;width:100%;min-height:40px;padding:0 24px 0 40px;text-align:left;font-size:14px}
.agent-work-row>small{color:var(--muted);font-size:12px;min-width:44px}
.agent-work-row:hover{background:var(--input)}
.work-stage-dot{height:12px;width:12px;border:1px solid #979ba1;border-radius:50%;display:block}
.work-stage-dot.hypotheses{border-color:#ad63db;background:#ad63db33}
.work-stage-dot.experimentation{border-color:#c5972b;background:#c5972b33}
.work-stage-dot.validation{border-color:#318750;background:#31875033}
.work-stage-dot.blocked{border-color:#dd5f6c;background:#dd5f6c33}
.work-stage-dot.reporting{border-color:#1478c6;background:#1478c6}
.agent-work-empty{padding:40px;text-align:center;color:var(--muted);font-size:13px}

</style>
