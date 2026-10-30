<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount, nextTick } from 'vue';
import { useCompute } from './api';
import { api } from '../../api/client';
import { workspaceId } from '../../api/session';
import { chatPath } from '../api';
import { formatBytes, formatCores } from './lib/compute-format';
import PaneIcon from './PaneIcon.vue';
const props = defineProps<{ projectId: string; title: string; disabled?: boolean }>();
const data = useCompute(() => props.projectId),
  menu = ref(''),
  saying = ref<string>(),
  reason = ref(''),
  error = ref(''),
  busy = ref(false),
  collapsed = ref(new Set<string>()),
  input = ref<HTMLInputElement[]>();
// The server reports a working kernel as busy; L2's rows call that state running.
const kernels = computed(() =>
  (data.data.value?.kernels ?? []).map((k) =>
    k.state === 'busy' ? { ...k, state: 'running' } : k,
  ),
);
const machines = computed(() =>
  (data.data.value?.machines ?? []).filter((m) =>
    kernels.value.some((k) => k.runtime_id === m.runtime_id),
  ),
);
const timer = setInterval(() => void data.refetch(), 4000);
onBeforeUnmount(() => clearInterval(timer));
watch(
  () => [workspaceId.value, props.projectId],
  () => {
    menu.value = '';
    saying.value = undefined;
    error.value = '';
  },
);
const ratio = (n: number | undefined, d: number | undefined) =>
  n !== undefined && d !== undefined && d > 0
    ? Math.min(100, Math.max(0, (n / d) * 100))
    : undefined;
const pair = (a?: number, b?: number) =>
  a === undefined && b === undefined ? '—' : `${formatBytes(a)} of ${formatBytes(b)}`;
function meters(m: (typeof machines.value)[number]) {
  const s = m.metrics;
  return [
    {
      label: 'Memory',
      value: pair(s?.mem_used, s?.mem_total),
      fraction: ratio(s?.mem_used, s?.mem_total),
    },
    {
      label: 'Processor',
      value: s ? `${formatCores(s.cpu_used)} of ${s.cores} cores` : '—',
      fraction: ratio(s?.cpu_used, s ? s.cores * 100 : undefined),
    },
    {
      label: 'Disk free',
      value: pair(s?.disk_free, s?.disk_total),
      fraction: ratio(s ? s.disk_total - s.disk_free : undefined, s?.disk_total),
    },
  ];
}
const mark = (k: string) =>
  k.toLowerCase().startsWith('python')
    ? 'Py'
    : k.toLowerCase().startsWith('julia')
      ? 'Jl'
      : k.toLowerCase() === 'r'
        ? 'R'
        : k;
function spark(values: number[], ceiling?: number) {
  return !values.length || !ceiling || ceiling <= 0
    ? ''
    : values
        .map((v) => '▁▂▃▄▅▆▇█'[Math.min(7, Math.round(Math.min(1, Math.max(0, v / ceiling)) * 7))])
        .join('');
}
async function ask(id: string) {
  menu.value = '';
  saying.value = id;
  reason.value = '';
  await nextTick();
  input.value?.[0]?.focus?.();
}
async function perform(chat: string, op: 'interrupt' | 'stop' | 'restart') {
  if (busy.value || props.disabled || (op === 'stop' && !reason.value.trim())) return;
  const ws = workspaceId.value,
    project = props.projectId;
  busy.value = true;
  error.value = '';
  try {
    await api(
      'POST',
      `${chatPath(project, chat)}/kernel`,
      { op, ...(op === 'stop' ? { reason: reason.value.trim() } : {}) },
      undefined,
      { workspace: ws },
    );
    if (ws === workspaceId.value && project === props.projectId) {
      saying.value = undefined;
      menu.value = '';
      await data.refetch();
    }
  } catch (e) {
    if (ws === workspaceId.value && project === props.projectId)
      error.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <section class="compute-panel" data-testid="compute-panel">
    <header class="art-header">
      <span class="art-title"><PaneIcon name="Chip" />Compute</span>
    </header>
    <div class="compute-panel-body">
      <p v-if="error || data.error.value" class="px-4 py-2 type-small text-danger" role="alert">
        {{ error || data.error.value?.message }}
      </p>
      <template v-if="data.data.value"
        ><p v-if="!kernels.length" class="compute-empty px-4 pb-4 type-small text-fg-subtle">
          No live kernels<span class="mt-1 block type-meta text-fg-tertiary"
            >Run a cell in this experiment and the machine it lands on appears here.</span
          >
        </p>
        <template v-else
          ><div
            v-for="machine in machines"
            :key="machine.runtime_id"
            data-testid="compute-meters"
            class="border-b border-line px-4 py-3"
          >
            <div class="mb-1.5 type-meta text-fg-subtle">{{ machine.device_name }}</div>
            <div class="flex gap-2">
              <div
                v-for="meter in meters(machine)"
                :key="meter.label"
                class="min-w-0 flex-1 rounded-lg border border-line-soft bg-surface px-3 py-2"
              >
                <div class="type-caption text-fg-subtle">{{ meter.label }}</div>
                <div class="mt-0.5 truncate type-body tabular-nums text-fg">{{ meter.value }}</div>
                <div class="mt-1.5 h-1 overflow-hidden rounded-full bg-surface-3">
                  <div
                    v-if="meter.fraction !== undefined"
                    class="h-full rounded-full bg-accent"
                    :style="{ width: `${meter.fraction}%` }"
                  />
                </div>
              </div>
            </div>
          </div>
          <ul class="list-none">
            <template v-for="machine in machines" :key="machine.runtime_id"
              ><li class="border-b border-line-soft">
                <button
                  type="button"
                  :aria-expanded="!collapsed.has(machine.runtime_id)"
                  class="flex w-full items-center gap-2 py-2 pl-5 pr-3 text-left hover:bg-surface-2"
                  @click="
                    collapsed.has(machine.runtime_id)
                      ? collapsed.delete(machine.runtime_id)
                      : collapsed.add(machine.runtime_id)
                  "
                >
                  <PaneIcon
                    name="ChevronDown"
                    width="11"
                    height="11"
                    class="shrink-0 text-fg-tertiary transition-transform"
                    :class="{ '-rotate-90': collapsed.has(machine.runtime_id) }"
                  /><span class="min-w-0 flex-1 truncate type-small text-fg">{{ title }}</span
                  ><span class="shrink-0 type-meta text-fg-tertiary"
                    >{{ kernels.filter((k) => k.runtime_id === machine.runtime_id).length }}
                    {{
                      kernels.filter((k) => k.runtime_id === machine.runtime_id).length === 1
                        ? 'kernel'
                        : 'kernels'
                    }}<template
                      v-if="
                        kernels.some(
                          (k) => k.runtime_id === machine.runtime_id && k.state === 'running',
                        )
                      "
                    >
                      ·
                      {{
                        kernels.filter(
                          (k) => k.runtime_id === machine.runtime_id && k.state === 'running',
                        ).length
                      }}
                      running</template
                    ></span
                  >
                </button>
              </li>
              <template v-if="!collapsed.has(machine.runtime_id)"
                ><li
                  v-for="kernel in kernels.filter((k) => k.runtime_id === machine.runtime_id)"
                  :key="kernel.chat_id"
                  class="grid grid-cols-[20px_minmax(0,1fr)_100px_68px_28px] items-center gap-2 border-b border-line-soft py-2 pl-9 pr-3"
                >
                  <span
                    aria-hidden="true"
                    class="grid h-5 w-5 place-items-center rounded border border-line bg-surface-2 type-data text-fg-tertiary"
                    >{{ mark(kernel.kernel) }}</span
                  ><span class="min-w-0"
                    ><span class="flex flex-wrap items-center gap-1.5"
                      ><span
                        class="h-1.5 w-1.5 shrink-0 rounded-full"
                        :class="
                          kernel.state === 'running'
                            ? 'bg-warn'
                            : kernel.state === 'idle'
                              ? 'bg-success'
                              : 'bg-fg-tertiary'
                        "
                      /><span
                        class="type-meta"
                        :class="kernel.state === 'running' ? 'text-warn' : 'text-fg-muted'"
                        >{{ kernel.state }}</span
                      ></span
                    ></span
                  ><span
                    v-if="saying === kernel.chat_id"
                    class="col-span-3 flex items-center gap-1.5"
                    ><input
                      ref="input"
                      :value="reason"
                      placeholder="Why? The agent reads this."
                      :aria-label="`Why the ${mark(kernel.kernel)} kernel is being stopped`"
                      class="min-w-0 flex-1 rounded-md border border-line bg-surface-2 px-2 py-1 type-small text-fg placeholder:text-fg-tertiary"
                      @input="reason = ($event.target as HTMLInputElement).value"
                      @keydown.escape="saying = undefined"
                      @keydown.enter="perform(kernel.chat_id, 'stop')"
                    /><button
                      type="button"
                      :disabled="!reason.trim() || busy || disabled"
                      class="shrink-0 rounded-md border border-line-strong px-2 py-1 type-small text-danger hover:bg-surface-2"
                      @click="perform(kernel.chat_id, 'stop')"
                    >
                      Stop
                    </button></span
                  ><template v-else
                    ><span class="flex min-w-0 flex-col items-end gap-0.5 type-meta text-fg-muted"
                      ><span class="whitespace-nowrap"
                        >{{ formatBytes(kernel.samples?.at(-1)?.rss)
                        }}<span class="ml-1 text-fg-tertiary">RSS</span></span
                      ><span
                        v-if="
                          spark(kernel.samples?.map((s) => s.rss) ?? [], machine.metrics?.mem_total)
                        "
                        aria-label="Memory over time"
                        class="type-data text-fg-tertiary"
                        >{{
                          spark(kernel.samples?.map((s) => s.rss) ?? [], machine.metrics?.mem_total)
                        }}</span
                      ></span
                    ><span class="flex min-w-0 flex-col items-end gap-0.5 type-meta text-fg-muted"
                      ><span class="whitespace-nowrap"
                        >{{ formatCores(kernel.samples?.at(-1)?.cpu)
                        }}<span class="ml-1 text-fg-tertiary">CPU</span></span
                      ><span
                        v-if="
                          spark(
                            kernel.samples?.map((s) => s.cpu) ?? [],
                            machine.metrics ? machine.metrics.cores * 100 : undefined,
                          )
                        "
                        aria-label="Processor over time"
                        class="type-data text-fg-tertiary"
                        >{{
                          spark(
                            kernel.samples?.map((s) => s.cpu) ?? [],
                            machine.metrics ? machine.metrics.cores * 100 : undefined,
                          )
                        }}</span
                      ></span
                    ><span class="relative flex justify-end"
                      ><button
                        type="button"
                        aria-haspopup="menu"
                        :aria-expanded="menu === kernel.chat_id"
                        :aria-label="`Actions for the ${mark(kernel.kernel)} kernel`"
                        :disabled="busy || disabled"
                        class="grid h-6 w-6 place-items-center rounded-md type-meta text-fg-tertiary hover:bg-surface-2 hover:text-fg"
                        @click="menu = menu === kernel.chat_id ? '' : kernel.chat_id"
                        @blur="menu = ''"
                      >
                        ⋯
                      </button>
                      <div
                        v-if="menu === kernel.chat_id"
                        role="menu"
                        aria-label="Kernel actions"
                        class="absolute right-0 top-7 z-20 flex min-w-[150px] flex-col rounded-lg border border-line bg-surface-3 p-1 shadow-xl"
                      >
                        <button
                          v-if="kernel.state === 'running'"
                          type="button"
                          role="menuitem"
                          class="rounded-md px-2 py-1.5 text-left type-body text-fg hover:bg-surface-4"
                          @mousedown="perform(kernel.chat_id, 'interrupt')"
                        >
                          Interrupt</button
                        ><button
                          v-if="kernel.state === 'running'"
                          type="button"
                          role="menuitem"
                          class="rounded-md px-2 py-1.5 text-left type-body text-fg hover:bg-surface-4"
                          @mousedown="ask(kernel.chat_id)"
                        >
                          Stop</button
                        ><button
                          type="button"
                          role="menuitem"
                          class="rounded-md px-2 py-1.5 text-left type-body text-fg hover:bg-surface-4"
                          @mousedown="perform(kernel.chat_id, 'restart')"
                        >
                          Restart
                        </button>
                      </div></span
                    ></template
                  >
                </li></template
              ></template
            >
          </ul></template
        ></template
      >
    </div>
  </section>
</template>
