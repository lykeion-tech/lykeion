import { createHash } from 'node:crypto';
import { existsSync, linkSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { tmpdir } from 'node:os';
import { execFileSync, spawn } from 'node:child_process';
import { once } from 'node:events';
import { pathToFileURL } from 'node:url';
import { describe, expect, it, vi } from 'vitest';
import { auditBundleSource, buildDesktopUpdater, readPackageFile } from '../scripts/desktop-updater-bundle.mjs';

describe('pinned desktop updater bundle', () => {
  it('rejects unresolved dynamic or non-allowlisted module loads', () => {
    expect(() => auditBundleSource('module.exports = (x) => require(x)')).toThrow();
    expect(() => auditBundleSource('require("unexpected-package")')).toThrow();
    expect(() => auditBundleSource('module.require("fs")')).toThrow();
    expect(() => auditBundleSource('const load = require; load("bad")')).toThrow();
    expect(() => auditBundleSource('module["require"]("bad")')).toThrow();
    expect(() => auditBundleSource('process.mainModule.require("bad")')).toThrow();
    expect(() => auditBundleSource('globalThis["require"]("bad")')).toThrow();
    expect(() => auditBundleSource('eval("require(\\"bad\\")")')).toThrow();
    expect(() => auditBundleSource('Function("return require")()')).toThrow();
    expect(() => auditBundleSource('require("module").createRequire("/tmp/x")("bad")')).toThrow();
    expect(() => auditBundleSource('require("node:fs")')).not.toThrow();
  });
  it('builds correctly when invoked outside the workspace', () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'lykeion-updater-cwd-'));
    try {
      const script = pathToFileURL(path.resolve('scripts/desktop-updater-bundle.mjs')).href;
      execFileSync(process.execPath, ['--input-type=module', '-e',
        'const {buildDesktopUpdater}=await import(process.argv[1]); await buildDesktopUpdater(process.argv[2],process.argv[3]);',
        script, process.cwd(), path.join(dir, 'updater.cjs')], { cwd: dir, stdio: 'pipe' });
      expect(readFileSync(path.join(dir, 'updater.cjs')).length).toBeGreaterThan(0);
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });
  it('preserves a previous bundle if notices cannot be published', async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'lykeion-updater-failure-'));
    try {
      const output = path.join(dir, 'updater.cjs');
      writeFileSync(output, 'previous');
      await expect(buildDesktopUpdater(process.cwd(), output, undefined, async () => { throw Error('test'); })).rejects.toThrow();
      expect(readFileSync(output, 'utf8')).toBe('previous');
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });
  it('rejects a notice symlink escaping the package', async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'lykeion-updater-notice-'));
    try {
      const pkg = path.join(dir, 'pkg'); mkdirSync(pkg);
      const secret = path.join(dir, 'outside'); writeFileSync(secret, 'private');
      symlinkSync(secret, path.join(pkg, 'LICENSE'));
      await expect(readPackageFile(pkg, 'LICENSE')).rejects.toThrow();
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });
  it('rejects a notice hardlinked to an outside file', async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'lykeion-updater-hardlink-'));
    try {
      const pkg = path.join(dir, 'pkg'); mkdirSync(pkg);
      const secret = path.join(dir, 'outside'); writeFileSync(secret, 'private');
      linkSync(secret, path.join(pkg, 'LICENSE'));
      await expect(readPackageFile(pkg, 'LICENSE')).rejects.toThrow();
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });
  it.skipIf(process.platform === 'win32')('rejects FIFO and oversized notice files', async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'lykeion-updater-files-'));
    try {
      execFileSync('mkfifo', [path.join(dir, 'fifo')]);
      writeFileSync(path.join(dir, 'large'), Buffer.alloc(1024 * 1024 + 1));
      await expect(readPackageFile(dir, 'fifo')).rejects.toThrow();
      await expect(readPackageFile(dir, 'large')).rejects.toThrow();
    } finally { rmSync(dir, { recursive: true, force: true }); }
  }, 5000);
  it('resolves a symlinked source root as used by macOS temporary paths', async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'lykeion-updater-root-'));
    try {
      const alias = path.join(dir, 'source');
      symlinkSync(process.cwd(), alias, 'dir');
      await expect(buildDesktopUpdater(alias, path.join(dir, 'updater.cjs'))).resolves.toBeUndefined();
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });
  it('publishes only a complete self-contained bundle during concurrent builds', async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'lykeion-updater-concurrent-'));
    try {
      const output = path.join(dir, 'updater.cjs');
      await Promise.all([buildDesktopUpdater(process.cwd(), output), buildDesktopUpdater(process.cwd(), output)]);
      const contents = readFileSync(output, 'utf8');
      expect(contents).toContain('electron-updater@6.8.11');
      expect(contents).toContain('lazy-val@1.0.5');
      expect(() => auditBundleSource(contents)).not.toThrow();
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });
  it.skipIf(process.platform === 'win32')('preserves previous output when killed before publication', async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'lykeion-updater-kill-'));
    const output = path.join(dir, 'updater.cjs');
    const marker = path.join(dir, 'ready');
    writeFileSync(output, 'previous');
    const script = pathToFileURL(path.resolve('scripts/desktop-updater-bundle.mjs')).href;
    const child = spawn(process.execPath, ['--input-type=module', '-e',
      'import {writeFileSync} from "node:fs"; const {buildDesktopUpdater}=await import(process.argv[1]); await buildDesktopUpdater(process.argv[2],process.argv[3],undefined,async()=>{writeFileSync(process.argv[4],"ready"); await new Promise(()=>setInterval(()=>{},1000));});',
      script, process.cwd(), output, marker], { stdio: 'ignore' });
    const exited = once(child, 'exit');
    try {
      await vi.waitFor(() => expect(existsSync(marker)).toBe(true), { timeout: 10000 });
      child.kill('SIGKILL');
      await exited;
      expect(readFileSync(output, 'utf8')).toBe('previous');
    } finally {
      if (child.exitCode === null && child.signalCode === null) { child.kill('SIGKILL'); await exited; }
      rmSync(dir, { recursive: true, force: true });
    }
  }, 15000);
  it('builds deterministic patched source and rejects source tampering', async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'lykeion-updater-test-'));
    try {
      const first = path.join(dir, 'one.cjs');
      const second = path.join(dir, 'two.cjs');
      await buildDesktopUpdater(process.cwd(), first);
      await buildDesktopUpdater(process.cwd(), second);
      const hash = (p: string) => createHash('sha256').update(readFileSync(p)).digest('hex');
      expect(hash(first)).toBe(hash(second));
      const licenses = readFileSync(first, 'utf8');
      expect(licenses).toContain('electron-updater@6.8.11');
      expect(licenses).toContain('lazy-val@1.0.5');
      const altered = path.join(dir, 'altered.js');
      writeFileSync(altered, 'tampered');
      await expect(buildDesktopUpdater(process.cwd(), path.join(dir, 'bad.cjs'), altered)).rejects.toThrow('Updater source integrity check failed');
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });
});
