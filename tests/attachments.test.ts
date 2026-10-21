import { describe, expect, it, vi } from 'vitest';
import { MAX_FILE, useAttachments } from '../apps/dashboard/src/lib/attachments';

const file = (name: string, size = 1) => ({ name, size }) as File;
const uploader = () =>
  vi.fn(async (f: File) => ({ id: `id-${f.name}`, name: f.name, size_bytes: f.size }));

describe('study attachments composer', () => {
  it('uploads picked files and sends their ids', async () => {
    const upload = uploader();
    const a = useAttachments(upload);
    await a.add([file('a.csv'), file('b.txt')]);
    expect(upload).toHaveBeenCalledTimes(2);
    expect(a.ids.value).toEqual(['id-a.csv', 'id-b.txt']);
    expect(a.error.value).toBe('');
  });

  it('refuses a file over 100 MB by name, without uploading', async () => {
    const upload = uploader();
    const a = useAttachments(upload);
    await a.add([file('big.bin', MAX_FILE + 1)]);
    expect(upload).not.toHaveBeenCalled();
    expect(a.error.value).toBe('big.bin: A file can be at most 100 MB');
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

  it('refuses files that would take the total over 500 MB', async () => {
    const upload = uploader();
    const a = useAttachments(upload);
    await a.add(Array.from({ length: 5 }, (_, i) => file(`m${i}`, 100 << 20)));
    expect(upload).toHaveBeenCalledTimes(5);
    await a.add([file('extra', 1)]);
    expect(upload).toHaveBeenCalledTimes(5);
    expect(a.error.value).toBe("A study's files can total at most 500 MB");
  });

  it('keeps a failed upload out of the ids', async () => {
    const a = useAttachments(async () => {
      throw new Error('boom');
    });
    await a.add([file('x')]);
    expect(a.ids.value).toEqual([]);
    expect(a.items.value[0]!.error).toBe('x: boom');
  });
});
