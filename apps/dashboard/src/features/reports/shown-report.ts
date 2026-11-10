import { ref } from 'vue';

// The study whose report is on screen, so the shell can give Aristotle it as
// context. A new id is a new arrival.
export const shownReport = ref<{ id: string; title: string } | null>(null);
