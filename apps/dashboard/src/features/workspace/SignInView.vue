<script setup lang="ts">
import { nextTick, ref } from 'vue';
import { api, ApiError, desktopGoogleSignIn } from '../../api/client';
import { notice } from '../../state/notice';

const email = ref('');
const step = ref<'email' | 'code'>('email');
const digits = ref<string[]>(Array(6).fill(''));
const boxes = ref<HTMLInputElement[]>([]);
const busy = ref(false);
const error = ref('');
const desktop = typeof window !== 'undefined' && Boolean(window.lykeionDesktop);
const googleAvailable = !desktop;
const localLogoutNotice = 'Signed out on this computer. The server did not confirm sign-out.';

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

async function google() {
  if (!desktop) {
    window.location.href = '/auth/google';
    return;
  }
  if (busy.value) return;
  busy.value = true;
  try {
    await desktopGoogleSignIn();
  } catch {
    error.value = 'Google sign-in was interrupted. Please try again.';
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <div class="signin">
    <form v-if="step === 'email'" class="signin-card" @submit.prevent="send">
      <span class="brand"><b>Λ</b> Lykeion</span>
      <h1>Sign in</h1>
      <p v-if="notice === localLogoutNotice" role="status">{{ localLogoutNotice }}</p>
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
      <div v-if="googleAvailable" class="or">or</div>
      <button v-if="googleAvailable" type="button" class="button" :disabled="busy" @click="google">
        <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 0 1-2.2 3.32v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.1z" fill="#4285F4" />
          <path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853" />
          <path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05" />
          <path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335" />
        </svg>
        Continue with Google
      </button>
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
  color: var(--primary-text);
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
