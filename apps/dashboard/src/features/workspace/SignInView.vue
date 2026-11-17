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
        <svg width="16" height="16" viewBox="0 0 48 48" aria-hidden="true">
          <path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z" />
          <path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z" />
          <path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z" />
          <path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.15 1.45-4.92 2.3-8.16 2.3-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z" />
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
