import { describe, expect, it, vi } from 'vitest';
import {
  formatSize,
  MAX_FILE,
  MAX_TOTAL,
  useAttachments,
} from './attachments';

const file = (name: string, size = 1) => ({ name, size }) as File;
const uploader = () =>
  vi.fn(async (f: File) => ({ id: `id-${f.name}`, name: f.name, size_bytes: f.size }));

describe('study attachments composer', () => {
  it('uses byte-safe GB limits and formats GB', () => {
    expect(MAX_FILE).toBe(5 * 2 ** 30);
    expect(MAX_TOTAL).toBe(10 * 2 ** 30);
    expect(formatSize(5 * 2 ** 30)).toBe('5 GB');
    expect(formatSize(1.5 * 2 ** 30)).toBe('1.5 GB');
  });
  it('uploads picked files and sends their ids', async () => {
    const upload = uploader();
    const a = useAttachments(upload);
    await a.add([file('a.csv'), file('b.txt')]);
    expect(upload).toHaveBeenCalledTimes(2);
    expect(a.ids.value).toEqual(['id-a.csv', 'id-b.txt']);
    expect(a.error.value).toBe('');
  });

  it('refuses a file over 5 GB by name, without uploading', async () => {
    const upload = uploader();
    const a = useAttachments(upload);
    await a.add([file('big.bin', 5 * 2 ** 30 + 1)]);
    expect(upload).not.toHaveBeenCalled();
    expect(a.error.value).toBe('big.bin: A file can be at most 5 GB');
  });

  it('refuses the 101st file without uploading it', async () => {
    const upload = uploader();
    const a = useAttachments(upload);
    await a.add(Array.from({ length: 100 }, (_, i) => file(`f${i}`)));
    expect(upload).toHaveBeenCalledTimes(100);
    await a.add([file('one-too-many')]);
    expect(upload).toHaveBeenCalledTimes(100);
    expect(a.error.value).toBe('A study can have at most 100 files');
    expect(a.ids.value).toHaveLength(100);
  });

  it('accepts the 10 GB boundary and refuses a further byte', async () => {
    const upload = uploader();
    const a = useAttachments(upload);
    await a.add([file('a', 5 * 2 ** 30), file('b', 5 * 2 ** 30)]);
    expect(upload).toHaveBeenCalledTimes(2);
    await a.add([file('extra', 1)]);
    expect(upload).toHaveBeenCalledTimes(2);
    expect(a.error.value).toBe("A study's files can total at most 10 GB");
  });

  it('keeps a failed upload out of the ids', async () => {
    const a = useAttachments(async () => {
      throw new Error('boom');
    });
    await a.add([file('x')]);
    expect(a.ids.value).toEqual([]);
    expect(a.items.value[0]!.error).toBe('x: boom');
    expect(a.failed.value).toBe(true);
    a.remove(a.items.value[0]!.key);
    expect(a.failed.value).toBe(false);
  });
});
