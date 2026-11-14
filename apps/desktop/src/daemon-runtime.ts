import { createHash, randomUUID } from 'node:crypto';
import { createReadStream, createWriteStream } from 'node:fs';
import { chmod, lstat, mkdir, readFile, readdir, rename, rm, writeFile } from 'node:fs/promises';
import { pipeline } from 'node:stream/promises';
import path from 'node:path';

const sha = /^[a-f0-9]{64}$/;

async function digest(file: string): Promise<string> {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(file)) hash.update(chunk);
  return hash.digest('hex');
}

// The daemon runs from a copy of the bundled lyk, kept by its SHA-256 outside
// the application. Replacing or unmounting the application on update never
// touches a running daemon, and the last copy that started stays on disk to
// fall back to when a new one fails to start.
export class DaemonRuntime {
  constructor(
    private readonly root: string,
    private readonly bundled: string,
    readonly sha256: string,
  ) {
    if (!sha.test(sha256)) throw new Error('Invalid bundled daemon digest');
  }
  private binary(hash: string) {
    return path.join(this.root, hash, 'lyk');
  }
  private async privateDir(dir: string) {
    await mkdir(dir, { recursive: true, mode: 0o700 });
    const info = await lstat(dir);
    if (!info.isDirectory() || (process.geteuid && info.uid !== process.geteuid()))
      throw new Error('Invalid desktop runtime folder');
    await chmod(dir, 0o700);
  }
  // verified returns the copy for hash when it is a plain file with that digest.
  async verified(hash: string): Promise<string | null> {
    if (!sha.test(hash)) return null;
    const file = this.binary(hash);
    try {
      const info = await lstat(file);
      if (!info.isFile() || (process.geteuid && info.uid !== process.geteuid()) || info.mode & 0o022)
        return null;
      return (await digest(file)) === hash ? file : null;
    } catch {
      return null;
    }
  }
  // install copies the bundled lyk into place and returns the verified copy.
  async install(): Promise<string> {
    const existing = await this.verified(this.sha256);
    if (existing) return existing;
    const dir = path.dirname(this.binary(this.sha256));
    await this.privateDir(this.root);
    await this.privateDir(dir);
    const temporary = path.join(dir, `.lyk-${randomUUID()}`);
    try {
      // A stream copy carries no extended attributes from the bundle.
      await pipeline(createReadStream(this.bundled), createWriteStream(temporary, { mode: 0o755, flags: 'wx' }));
      if ((await digest(temporary)) !== this.sha256)
        throw new Error('The bundled Lykeion runtime is missing or invalid. Reinstall the app.');
      await rename(temporary, this.binary(this.sha256));
    } finally {
      await rm(temporary, { force: true });
    }
    const installed = await this.verified(this.sha256);
    if (!installed) throw new Error('The Lykeion runtime could not be installed');
    return installed;
  }
  // lastGood is the copy that last started, if it is still intact.
  async lastGood(): Promise<{ sha256: string; path: string } | null> {
    try {
      const state = JSON.parse(await readFile(path.join(this.root, 'started.json'), 'utf8'));
      const file = await this.verified(state?.sha256);
      return file ? { sha256: state.sha256, path: file } : null;
    } catch {
      return null;
    }
  }
  // started records hash as the last copy that started and removes copies
  // older than the one it replaces.
  async started(hash: string): Promise<void> {
    const previous = (await this.lastGood())?.sha256;
    await this.privateDir(this.root);
    const temporary = path.join(this.root, `.started-${randomUUID()}`);
    await writeFile(temporary, JSON.stringify({ sha256: hash }), { mode: 0o600, flag: 'wx' });
    await rename(temporary, path.join(this.root, 'started.json'));
    for (const name of await readdir(this.root)) {
      if (sha.test(name) && name !== hash && name !== previous)
        await rm(path.join(this.root, name), { recursive: true, force: true });
    }
  }
}
