import { createHash } from 'node:crypto';
import { mkdir, open, unlink } from 'node:fs/promises';
import path from 'node:path';

// Marker never contains credentials. A surviving marker means a cookie write may
// have been interrupted, so startup clears the partition before displaying it.
export class DesktopAuthRecovery {
  readonly path: string;
  private marked = false;
  private readonly directory: string;
  constructor(userData: string, origin: string) {
    this.directory = path.join(userData, 'auth-recovery');
    this.path = path.join(this.directory, createHash('sha256').update(origin).digest('hex'));
  }
  private async syncDirectory(directory = this.directory): Promise<void> {
    const dir = await open(directory, 'r');
    try {
      await dir.sync();
    } finally {
      await dir.close();
    }
  }
  async mark(): Promise<void> {
    if (this.marked) return;
    await mkdir(this.directory, { recursive: true, mode: 0o700 });
    await this.syncDirectory(path.dirname(this.directory));
    const file = await open(this.path, 'wx', 0o600);
    try {
      await file.writeFile('pending\n');
      await file.sync();
    } finally {
      await file.close();
    }
    await this.syncDirectory();
    this.marked = true;
  }
  async recover(clear: () => Promise<void>, verifiedEmpty: () => Promise<boolean>): Promise<void> {
    try {
      const file = await open(this.path, 'r');
      await file.close();
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code === 'ENOENT') return;
      throw error;
    }
    await clear();
    if (!(await verifiedEmpty())) throw new Error('Authentication recovery could not be verified');
    await unlink(this.path);
    await this.syncDirectory();
    this.marked = false;
  }
  async cleanShutdown(): Promise<void> {
    if (!this.marked) return;
    await unlink(this.path);
    await this.syncDirectory();
    this.marked = false;
  }
}
