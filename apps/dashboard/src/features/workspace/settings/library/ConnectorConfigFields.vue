<script lang="ts">
import type { WorkspaceMcpConfig } from '../../../../api/library';
export type ConnectorFields = {
  name: string;
  transport: 'url' | 'command';
  url: string;
  command: string;
  args: string[];
  headers: { name: string; value: string }[];
  env: { name: string; value: string }[];
  keyless: boolean;
};
export function emptyConnector(name = '', transport: 'url' | 'command' = 'url'): ConnectorFields {
  return { name, transport, url: '', command: '', args: [], headers: [], env: [], keyless: false };
}
export function connectorInput(value: ConnectorFields): {
  name: string;
  config: WorkspaceMcpConfig;
} {
  return {
    name: value.name.trim(),
    config:
      value.transport === 'url'
        ? {
            url: value.url,
            headers: Object.fromEntries(value.headers.map((p) => [p.name, p.value])),
          }
        : {
            command: value.command,
            args: [...value.args],
            env: Object.fromEntries(value.env.map((p) => [p.name, p.value])),
            keyless: value.keyless,
          },
  };
}
export function connectorProblem(value: ConnectorFields) {
  if (!/^[a-zA-Z0-9_-]+$/.test(value.name.trim()))
    return 'Use letters, digits, hyphens or underscores for the name.';
  if (value.transport === 'url') {
    try {
      const u = new URL(value.url);
      if (u.protocol !== 'https:' || !u.host) return 'An https MCP URL is required.';
    } catch {
      return 'An https MCP URL is required.';
    }
  } else {
    if (!value.command || value.command.includes('/') || value.command.includes('..'))
      return 'Name a program, not a path.';
    if (value.env.some((p) => /(key|token|secret|password)/i.test(p.name)))
      return 'An environment name looks like a secret.';
  }
  const pairs = value.transport === 'url' ? value.headers : value.env;
  if (pairs.some((p) => !p.name.trim())) return 'Name each row.';
  const names = pairs.map((p) => (value.transport === 'url' ? p.name.toLowerCase() : p.name));
  if (new Set(names).size !== pairs.length) return 'Each row needs a unique name.';
  return '';
}
</script>
<script setup lang="ts">
import Icon from '../../../../ui/Icon.vue';
const value = defineModel<ConnectorFields>({ required: true });
defineProps<{ disabled?: boolean }>();
</script>
<template>
  <label class="field"
    >Connector name<input
      v-model="value.name"
      aria-label="Name"
      placeholder="e.g. lab-search"
      :disabled="disabled"
    /><small>A unique name in this workspace.</small></label
  >
  <span class="label">Connection</span>
  <div class="transports">
    <button
      v-for="transport in ['url', 'command'] as const"
      :key="transport"
      type="button"
      class="transport"
      :class="{ active: value.transport === transport }"
      :aria-label="transport === 'url' ? 'URL' : 'Command'"
      :aria-pressed="value.transport === transport"
      :disabled="disabled"
      @click="value.transport = transport"
    >
      <Icon :name="transport === 'url' ? 'link' : 'command'" :size="20" /><span
        ><strong>{{ transport === 'url' ? 'URL' : 'Command' }}</strong
        ><small>{{
          transport === 'url' ? 'Connect to a remote server' : 'Start a server on a machine'
        }}</small></span
      >
    </button>
  </div>
  <template v-if="value.transport === 'url'"
    ><label class="field"
      >Connector URL<input
        v-model="value.url"
        aria-label="MCP URL"
        placeholder="https://mcp.example.com/mcp"
        autocomplete="off"
        :disabled="disabled"
      /><small>A Streamable HTTP MCP endpoint.</small></label
    >
    <div class="subhead">
      <b>HTTP headers</b
      ><button
        type="button"
        class="text-button"
        :disabled="disabled"
        @click="value.headers.push({ name: '', value: '' })"
      >
        <Icon name="plus" :size="12" />Add HTTP header
      </button>
    </div>
    <p class="hint">Sent with requests to this server.</p>
    <div v-for="(pair, i) in value.headers" :key="i" class="pair">
      <input
        v-model="pair.name"
        :aria-label="`Header name ${i + 1}`"
        placeholder="Header name"
        :disabled="disabled"
      /><input
        v-model="pair.value"
        type="password"
        :aria-label="`Header value ${i + 1}`"
        placeholder="Value"
        autocomplete="new-password"
        :disabled="disabled"
      /><button
        type="button"
        class="text-button"
        :aria-label="`Remove header ${i + 1}`"
        :disabled="disabled"
        @click="value.headers.splice(i, 1)"
      >
        <Icon name="close" :size="16" />
      </button></div
  ></template>
  <template v-else
    ><label class="field"
      >Command<input
        v-model="value.command"
        aria-label="Command"
        placeholder="npx"
        :disabled="disabled"
    /></label>
    <div class="subhead">
      <b>Startup arguments</b
      ><button type="button" class="text-button" :disabled="disabled" @click="value.args.push('')">
        <Icon name="plus" :size="12" />Add argument
      </button>
    </div>
    <p class="hint">Passed to the command in this order.</p>
    <div v-for="(_, i) in value.args" :key="i" class="pair">
      <input
        v-model="value.args[i]"
        :aria-label="`Argument ${i + 1}`"
        placeholder="e.g. -y"
        :disabled="disabled"
      /><button
        type="button"
        class="text-button"
        :aria-label="`Remove argument ${i + 1}`"
        :disabled="disabled"
        @click="value.args.splice(i, 1)"
      >
        <Icon name="close" :size="16" />
      </button>
    </div>
    <div class="subhead">
      <b>Environment variables</b
      ><button
        type="button"
        class="text-button"
        :disabled="disabled"
        @click="value.env.push({ name: '', value: '' })"
      >
        <Icon name="plus" :size="12" />Add environment variable
      </button>
    </div>
    <p class="hint">
      Command arguments and environment values must contain no secrets. The command runs on the
      machine claiming the run.
    </p>
    <label
      ><input v-model="value.keyless" type="checkbox" :disabled="disabled" /> I confirm that this
      command, its arguments, and environment contain no credentials.</label
    >
    <div v-for="(pair, i) in value.env" :key="i" class="pair">
      <input
        v-model="pair.name"
        :aria-label="`Variable name ${i + 1}`"
        placeholder="Variable name"
        :disabled="disabled"
      /><input
        v-model="pair.value"
        type="password"
        :aria-label="`Variable value ${i + 1}`"
        placeholder="Value"
        :disabled="disabled"
      /><button
        type="button"
        class="text-button"
        :aria-label="`Remove variable ${i + 1}`"
        :disabled="disabled"
        @click="value.env.splice(i, 1)"
      >
        <Icon name="close" :size="16" />
      </button></div
  ></template>
</template>
<style scoped>
.field {
  display: block;
  font-size: 12px;
  margin-bottom: 22px;
}
.field input {
  display: block;
  margin-top: 7px;
}
.field small,
.hint {
  display: block;
  font-size: 12px;
  color: var(--muted);
  line-height: 1.5;
  margin: 6px 0 0;
}
.label {
  font-size: 12px;
}
.transports {
  display: flex;
  gap: 12px;
  margin: 12px 0 24px;
}
.transport {
  display: flex;
  flex: 1;
  gap: 12px;
  align-items: center;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: none;
  color: var(--text);
  text-align: left;
  padding: 14px 16px;
  cursor: pointer;
}
.transport.active {
  border-color: var(--primary);
  background: color-mix(in srgb, var(--primary) 5%, transparent);
}
.transport strong {
  display: block;
  font-size: 13px;
  font-weight: 500;
}
.transport small {
  display: block;
  font-size: 12px;
  color: var(--muted);
  margin-top: 3px;
}
input {
  width: 100%;
  min-width: 0;
  background: var(--input);
  border: 1px solid var(--border);
  border-radius: 7px;
  padding: 8px 10px;
  color: var(--text);
  font: inherit;
  font-size: 12px;
  height: 34px;
}
.subhead {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin: 20px 0 8px;
  font-size: 12px;
}
.subhead b {
  font-weight: 500;
}
.pair {
  display: flex;
  gap: 8px;
  margin-top: 8px;
}
.pair input {
  flex: 1;
}
.text-button {
  display: inline-flex;
  gap: 4px;
  align-items: center;
  border: 0;
  background: none;
  color: var(--text);
  padding: 4px 0;
  font-size: 12px;
  cursor: pointer;
}
button:focus-visible,
input:focus-visible {
  outline: 2px solid var(--primary-text);
  outline-offset: 2px;
}
</style>
