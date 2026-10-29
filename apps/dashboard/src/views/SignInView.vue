<script setup lang="ts">
import { nextTick, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useQueryClient } from '@tanstack/vue-query';
import { api, ApiError } from '../api/client';
import { authenticationRestored, finishAuthenticationRecovery } from '../api/auth-loss';
import { workspaceId } from '../api/session';
import { readText, writeText } from '../storage';

const router = useRouter();
const client = useQueryClient();
const email = ref('');
const step = ref<'email' | 'code'>('email');
const digits = ref<string[]>(Array(6).fill(''));
const boxes = ref<HTMLInputElement[]>([]);
const busy = ref(false);
const error = ref('');

async function send() {
  if (busy.value || !email.value.includes('@')) return;
  busy.value = true;
  error.value = '';
  try {
    await api('POST', '/auth/send-code', { email: email.value.trim() });
    step.value = 'code';
    digits.value = Array(6).fill('');
    await nextTick();
    boxes.value[0]?.focus();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}

// Typing moves to the next box; a pasted code fills the boxes from this one on.
function input(i: number, event: Event) {
  const typed = (event.target as HTMLInputElement).value.replace(/\D/g, '');
  const next = [...digits.value];
  if (!typed) next[i] = '';
  for (let k = 0; k < typed.length && i + k < 6; k++) next[i + k] = typed[k]!;
  digits.value = next;
  if (typed) boxes.value[Math.min(5, i + typed.length)]?.focus();
  if (next.every(Boolean)) void verify();
}
function back(i: number) {
  if (!digits.value[i] && i > 0) boxes.value[i - 1]?.focus();
}

async function verify() {
  const code = digits.value.join('');
  if (busy.value || code.length !== 6) return;
  busy.value = true;
  error.value = '';
  try {
    await api('POST', '/auth/verify-code', { email: email.value.trim(), code });
    // Reset mounted observers so this session loads its own identity and workspaces.
    await client.cancelQueries();
    workspaceId.value = '';
    await nextTick();
    authenticationRestored();
    await client.resetQueries();
    const invite = readText('lykeion.invite', 'session');
    if (invite) {
      writeText('lykeion.invite', '', 'session');
      await router.replace('/invite/' + invite);
    } else await router.replace('/');
    finishAuthenticationRecovery();
  } catch (e) {
    error.value =
      e instanceof ApiError && e.status === 401
        ? 'That code is wrong or has expired.'
        : (e as Error).message;
    digits.value = Array(6).fill('');
    await nextTick();
    boxes.value[0]?.focus();
  } finally {
    busy.value = false;
  }
}

function google() {
  window.location.href = '/auth/google';
}
</script>
<template>
  <div class="signin">
    <form v-if="step === 'email'" class="signin-card" @submit.prevent="send">
      <span class="brand"><b>Λ</b> Lykeion</span>
      <h1>Sign in</h1>
      <p class="sub">Use your work email; we'll send a 6-digit code.</p>
      <input
        v-model="email"
        type="email"
        autocomplete="email"
        aria-label="Email"
        placeholder="you@lab.org"
        required
        autofocus
      />
      <button class="button primary" :disabled="busy">Continue with email</button>
      <div class="or">or</div>
      <button type="button" class="button" @click="google">Continue with Google</button>
      <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
    </form>
    <form v-else class="signin-card" @submit.prevent="verify">
      <span class="brand"><b>Λ</b> Lykeion</span>
      <h1>Check your email</h1>
      <p class="sub">We sent a code to {{ email }}. It expires in 10 minutes.</p>
      <div class="codes">
        <input
          v-for="(digit, i) in digits"
          :key="i"
          :ref="
            (el) => {
              if (el) boxes[i] = el as HTMLInputElement;
            }
          "
          :value="digit"
          inputmode="numeric"
          autocomplete="one-time-code"
          maxlength="6"
          :aria-label="'Digit ' + (i + 1)"
          @input="input(i, $event)"
          @keydown.backspace="back(i)"
        />
      </div>
      <button class="button primary" :disabled="busy || digits.some((d) => !d)">Sign in</button>
      <p class="links">
        <button type="button" class="text-button" :disabled="busy" @click="send">Send again</button>
        ·
        <button type="button" class="text-button" @click="step = 'email'">Use another email</button>
      </p>
      <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
    </form>
  </div>
</template>
<style scoped>
.signin {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--canvas);
}
.signin-card {
  width: 380px;
  background: var(--surface);
  border-radius: 14px;
  padding: 30px 30px 24px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.brand b {
  color: var(--primary);
}
.signin-card h1 {
  font-size: 20px;
  font-weight: 600;
  margin-top: 10px;
}
.sub {
  color: var(--muted);
  font-size: 13.5px;
  margin-bottom: 8px;
}
.signin-card > input {
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 7px 10px;
  font: inherit;
}
.or {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--muted);
  font-size: 12px;
}
.or::before,
.or::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--border);
}
.codes {
  display: flex;
  gap: 6px;
}
.codes input {
  width: 44px;
  height: 50px;
  border-radius: 8px;
  border: 1px solid var(--border);
  text-align: center;
  font-size: 20px;
}
.links {
  text-align: center;
  font-size: 12.5px;
  color: var(--muted);
}
.signin-card .inline-error {
  margin: 0;
}
</style>
