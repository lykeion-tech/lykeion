import { computed, ref } from 'vue';

export const MAX_FILE = 5 * 2 ** 30;
export const MAX_FILES = 100;
export const MAX_TOTAL = 10 * 2 ** 30;

export interface Uploaded {
  id: string;
  name: string;
  size_bytes: number;
}
export type Upload = (file: File, onProgress: (fraction: number) => void) => Promise<Uploaded>;
export interface Picked {
  key: number;
  name: string;
  size: number;
  progress: number;
  id?: string;
  error?: string;
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1 << 20) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 2 ** 30) return `${(bytes / (1 << 20)).toFixed(1)} MB`;
  return `${Number((bytes / 2 ** 30).toFixed(1))} GB`;
}

// The files picked for a new study: each uploads as soon as it passes the
// limits, and a refused file is never sent.
export function useAttachments(upload: Upload) {
  const items = ref<Picked[]>([]);
  const error = ref('');
  let next = 0;

  const ids = computed(() => items.value.flatMap((i) => (i.id ? [i.id] : [])));
  const failed = computed(() => items.value.some((i) => i.error));
  const uploading = computed(() => items.value.some((i) => !i.id && !i.error));

  function refusal(file: File): string {
    if (file.size > MAX_FILE) return `${file.name}: A file can be at most 5 GB`;
    if (items.value.length >= MAX_FILES) return 'A study can have at most 100 files';
    if (items.value.reduce((n, i) => n + i.size, 0) + file.size > MAX_TOTAL)
      return "A study's files can total at most 10 GB";
    return '';
  }

  async function add(files: File[]) {
    error.value = '';
    const sending: Promise<void>[] = [];
    for (const file of files) {
      const refused = refusal(file);
      if (refused) {
        error.value ||= refused;
        continue;
      }
      items.value.push({ key: next++, name: file.name, size: file.size, progress: 0 });
      const item = items.value[items.value.length - 1]!;
      sending.push(
        upload(file, (f) => (item.progress = f)).then(
          (done) => {
            item.id = done.id;
            item.progress = 1;
          },
          (e: Error) => {
            item.error = `${file.name}: ${e.message}`;
          },
        ),
      );
    }
    await Promise.all(sending);
  }

  const remove = (key: number) => (items.value = items.value.filter((i) => i.key !== key));
  const clear = () => {
    items.value = [];
    error.value = '';
  };
  return { items, error, ids, uploading, failed, add, remove, clear };
}
