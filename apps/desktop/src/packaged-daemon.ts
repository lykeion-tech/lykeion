import { createHash } from 'node:crypto';
import { constants } from 'node:fs';
import { access, lstat, open, realpath } from 'node:fs/promises';
import path from 'node:path';

export async function trustedPackageAncestors(start: string, resources: string, uid: number) {
  let dir = start;
  for (;;) {
    const info = await lstat(dir);
    if (!info.isDirectory() || (info.uid !== 0 && info.uid !== uid)) return false;
    const outsideResources = dir !== resources && dir !== path.join(resources, 'bin');
    // The system administrator group (gid 80) manages the canonical macOS application directory.
    const applications =
      process.platform === 'darwin' &&
      dir === '/Applications' &&
      info.uid === 0 &&
      info.gid === 80 &&
      (info.mode & 0o7777) === 0o775;
    const stickyParent = outsideResources && info.uid === 0 && (info.mode & 0o1000) !== 0;
    if ((info.mode & 0o6022) !== 0 && !applications && !stickyParent) return false;
    const parent = path.dirname(dir);
    if (parent === dir) return true;
    dir = parent;
  }
}

// The manifest is read from the same application archive as the running main process.
export function packagedDaemonVerifier(resources: string, manifest: unknown, version: string) {
  let verified = '';
  return async (): Promise<boolean> => {
    if (
      !manifest ||
      typeof manifest !== 'object' ||
      !('version' in manifest) ||
      manifest.version !== version ||
      !('platform' in manifest) ||
      manifest.platform !== process.platform ||
      !['darwin', 'linux'].includes(process.platform) ||
      !('arch' in manifest) ||
      manifest.arch !== process.arch ||
      !('daemonSHA256' in manifest) ||
      typeof manifest.daemonSHA256 !== 'string' ||
      !/^[a-f0-9]{64}$/.test(manifest.daemonSHA256)
    )
      return false;
    const uid = process.geteuid?.();
    if (uid === undefined) return false;
    const trusted = (owner: number, mode: number) =>
      (owner === 0 || owner === uid) && !(mode & 0o6022);
    try {
      const bin = path.join(resources, 'bin');
      const file = path.join(bin, 'lyk');
      if ((await realpath(file)) !== file) return false;
      if (!(await trustedPackageAncestors(bin, resources, uid))) return false;
      const handle = await open(
        file,
        constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK,
      );
      try {
        const info = await handle.stat({ bigint: true });
        if (!info.isFile() || !trusted(Number(info.uid), Number(info.mode)) || info.nlink !== 1n)
          return false;
        await access(file, constants.X_OK);
        const stamp = [info.dev, info.ino, info.size, info.ctimeNs, info.mtimeNs].join(':');
        if (verified !== stamp) {
          const hash = createHash('sha256');
          for await (const chunk of handle.createReadStream({ autoClose: false }))
            hash.update(chunk);
          if (hash.digest('hex') !== manifest.daemonSHA256) return false;
        }
        const now = await lstat(file, { bigint: true });
        if (now.ino !== info.ino || now.dev !== info.dev || now.ctimeNs !== info.ctimeNs)
          return false;
        verified = stamp;
        return true;
      } finally {
        await handle.close();
      }
    } catch {
      return false;
    }
  };
}
