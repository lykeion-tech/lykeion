<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue';
import { useMe, useMembers, useRuntimes } from '../../api/queries';
import { studyMachines } from './machines';
import Icon from '../../ui/Icon.vue';
import CliIcon from '../coresearch/CliIcon.vue';
import { backendLabel } from '../../lib/backends';

const value = defineModel<string>({ default: '' });
defineProps<{ disabled?: boolean; chip?: boolean }>();
const emit = defineEmits<{ open: [] }>();
const me = useMe();
const members = useMembers();
const runtimes = useRuntimes();
const root = ref<HTMLElement>();
const trigger = ref<HTMLButtonElement>();
const open = ref(false);
const machines = computed(() => studyMachines(runtimes.data.value ?? [], me.data.value?.id ?? ''));
function ownerLabel(owner: string) {
  const member = members.data.value?.find((m) => m.user_id === owner);
  return member?.name || member?.email || owner;
}
function machineLabel(machine: (typeof machines.value)[number]) {
  return machine.ownerId === me.data.value?.id
    ? machine.name
    : `${machine.name} (${ownerLabel(machine.ownerId)})`;
}
const selected = computed(() => {
  if (!value.value) return 'Machines';
  const machine = machines.value.find((item) => item.id === value.value);
  return machine ? machineLabel(machine) : 'Unavailable machine';
});
async function show() {
  open.value = !open.value;
  if (open.value) {
    emit('open');
    await nextTick();
    root.value
      ?.querySelector<HTMLButtonElement>(
        '[role="menuitemradio"][aria-checked="true"]:not(:disabled), [role="menuitemradio"]:not(:disabled)',
      )
      ?.focus();
  }
}
function choose(id: string) {
  value.value = id;
  open.value = false;
  nextTick(() => trigger.value?.focus());
}
function keydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && open.value) {
    event.preventDefault();
    event.stopPropagation();
    open.value = false;
    trigger.value?.focus();
  } else if (open.value && ['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
    const items = Array.from(
      root.value?.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]:not(:disabled)') ??
        [],
    );
    if (!items.length) return;
    event.preventDefault();
    event.stopPropagation();
    const index = items.indexOf(document.activeElement as HTMLButtonElement);
    const next =
      event.key === 'Home'
        ? 0
        : event.key === 'End'
          ? items.length - 1
          : event.key === 'ArrowDown'
            ? (index + 1) % items.length
            : (index + items.length - 1) % items.length;
    items[next]?.focus();
  }
}
function outside(event: PointerEvent) {
  if (root.value && !root.value.contains(event.target as Node)) open.value = false;
}
onMounted(() => document.addEventListener('pointerdown', outside));
onUnmounted(() => document.removeEventListener('pointerdown', outside));
</script>
<template>
  <div ref="root" class="machine-picker type-caption" @keydown="keydown">
    <span v-if="!chip">Machine</span>
    <button
      ref="trigger"
      type="button"
      class="machine-trigger"
      aria-label="Machines"
      aria-haspopup="menu"
      :aria-expanded="open"
      :disabled="disabled"
      @click="show"
    >
      <Icon name="monitor" :size="14" /><span>{{ selected }}</span>
    </button>
    <div v-if="open" class="machine-menu" role="menu" aria-label="Choose machine">
      <button
        v-for="machine in machines"
        :key="machine.id"
        type="button"
        role="menuitemradio"
        :aria-checked="value === machine.id"
        @click="choose(machine.id)"
      >
        <span class="machine-details">
          <span class="machine-heading">
            <Icon name="monitor" :size="14" />
            <span>{{ machineLabel(machine) }}</span>
            <small :class="{ online: machine.online }">{{
              machine.online ? 'Online' : 'Offline'
            }}</small>
          </span>
          <span class="machine-runtimes">
            <span v-for="runtime in machine.runtimes" :key="runtime.id" class="machine-runtime">
              <i :class="runtime.harness" aria-hidden="true"
                ><CliIcon
                  v-if="runtime.harness === 'claude' || runtime.harness === 'codex'"
                  :cli="runtime.harness"
                /><template v-else>{{ runtime.harness[0] }}</template></i
              >
              {{ backendLabel(runtime.harness) }}
            </span>
          </span>
        </span>
        <Icon v-if="value === machine.id" name="check" :size="14" />
      </button>
      <button
        v-if="value && !machines.some((m) => m.id === value)"
        type="button"
        role="menuitemradio"
        aria-checked="true"
        disabled
      >
        <span>Unavailable machine</span><Icon name="check" :size="14" />
      </button>
    </div>
  </div>
</template>
<style scoped>
.machine-picker {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: var(--muted);
}
.machine-trigger {
  display: flex;
  align-items: center;
  gap: 6px;
  max-width: 160px;
  min-width: 0;
  height: 26px;
  padding: 0 8px;
  border: 1px solid var(--control-border);
  border-radius: 7px;
  color: var(--text);
  background: transparent;
  font: inherit;
  font-size: 12px;
}
.machine-trigger span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.machine-trigger .icon {
  flex: none;
}
.machine-trigger:hover:not(:disabled) {
  background: var(--fill-chip);
}
.machine-menu {
  position: absolute;
  z-index: 80;
  right: 0;
  bottom: calc(100% + 6px);
  width: max-content;
  min-width: 180px;
  max-width: min(280px, 80vw);
  max-height: min(300px, 50vh);
  overflow: auto;
  padding: 5px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--fill-menu);
  box-shadow: 0 6px 22px #0002;
  color: var(--text);
}
.machine-menu button {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 31px;
  padding: 6px 8px;
  border-radius: 4px;
  text-align: left;
  font-size: 13px;
}
.machine-menu button:hover:not(:disabled),
.machine-menu button:focus-visible {
  background: var(--input);
}
.machine-menu button span {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.machine-menu button .icon {
  flex: none;
}
.machine-menu .machine-details {
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.machine-menu .machine-heading,
.machine-menu .machine-runtimes,
.machine-menu .machine-runtime {
  display: flex;
  align-items: center;
  gap: 6px;
}
.machine-menu .machine-heading > span {
  flex: 1;
}
.machine-heading small {
  flex: none;
  color: var(--muted);
  font-size: 11px;
}
.machine-heading small.online {
  color: var(--success, #278241);
}
.machine-menu .machine-runtimes {
  flex-wrap: wrap;
  gap: 4px 12px;
  padding-left: 20px;
  color: var(--muted);
  font-size: 11px;
}
.machine-menu .machine-runtime {
  flex: none;
}
.machine-runtime i {
  font-size: 15px;
  font-style: normal;
  line-height: 1;
}
.machine-runtime i.claude {
  color: #d78b70;
}
</style>
