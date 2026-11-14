import {
  constants,
  closeSync,
  fsyncSync,
  lstatSync,
  mkdirSync,
  openSync,
  unlinkSync,
} from 'node:fs';
import { createHash } from 'node:crypto';
import path from 'node:path';

export class LogoutIntent {
  readonly directory: string;
  readonly path: string;

  constructor(userData: string, origin: string, kind: 'logout' | 'verification' = 'logout') {
    this.directory = path.join(userData, kind + '-intents');
    this.path = path.join(this.directory, createHash('sha256').update(origin).digest('hex'));
  }

  private syncDirectory(directory: string): void {
    const fd = openSync(directory, constants.O_RDONLY | constants.O_NOFOLLOW);
    try {
      fsyncSync(fd);
    } finally {
      closeSync(fd);
    }
  }

  private checkDirectory(): void {
    const info = lstatSync(this.directory);
    if (!info.isDirectory() || info.isSymbolicLink() || (info.mode & 0o077) !== 0)
      throw new Error('Invalid logout recovery directory');
  }

  exists(): boolean {
    try {
      this.checkDirectory();
      const info = lstatSync(this.path);
      if (!info.isFile() || info.isSymbolicLink() || (info.mode & 0o077) !== 0)
        throw new Error('Invalid logout recovery marker');
      return true;
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code === 'ENOENT') return false;
      throw error;
    }
  }

  mark(): void {
    mkdirSync(this.directory, { recursive: true, mode: 0o700 });
    this.checkDirectory();
    let fd: number;
    try {
      fd = openSync(
        this.path,
        constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW,
        0o600,
      );
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'EEXIST' || !this.exists()) throw error;
      fd = openSync(this.path, constants.O_RDONLY | constants.O_NOFOLLOW);
    }
    try {
      fsyncSync(fd);
    } finally {
      closeSync(fd);
    }
    this.syncDirectory(this.directory);
    this.syncDirectory(path.dirname(this.directory));
  }

  clear(): void {
    if (!this.exists()) return;
    unlinkSync(this.path);
    this.syncDirectory(this.directory);
  }
}
