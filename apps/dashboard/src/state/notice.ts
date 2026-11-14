import { ref } from 'vue';

// The shell's one toast line.
export const notice = ref('');
let timer: ReturnType<typeof setTimeout>;
export function notify(message: string) {
  notice.value = message;
  clearTimeout(timer);
  timer = setTimeout(() => (notice.value = ''), 7000);
}
