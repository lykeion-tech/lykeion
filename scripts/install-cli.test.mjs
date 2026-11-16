import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, symlinkSync, readdirSync } from 'node:fs';
import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const installer = path.resolve(path.dirname(fileURLToPath(import.meta.url)), 'install.sh');
function fixture() {
  const dir = mkdtempSync(path.join(tmpdir(), 'lyk-install-test-'));
  const bin = path.join(dir, 'bin'); const dest = path.join(dir, 'destination'); const home = path.join(dir, 'home');
  for (const p of [bin, dest, home]) mkdirSync(p);
  writeFileSync(path.join(dir, 'lyk'), '#!/bin/sh\necho installed\n', { mode: 0o755 });
  execFileSync('tar', ['-czf', path.join(dir, 'fixture.tar.gz'), '-C', dir, 'lyk']);
  const digest = createHash('sha256').update(readFileSync(path.join(dir, 'fixture.tar.gz'))).digest('hex');
  const names = ['darwin-amd64', 'darwin-arm64', 'linux-amd64', 'linux-arm64'].map(x => `lyk-cli-1.2.3-${x}.tar.gz`);
  writeFileSync(path.join(dir, 'SHA256SUMS'), names.map(x => `${digest}  ${x}`).join('\n') + '\n');
  writeFileSync(path.join(bin, 'uname'), '#!/bin/sh\ncase "$1" in -s) echo "$TEST_OS";; -m) echo "$TEST_ARCH";; esac\n', { mode: 0o755 });
  writeFileSync(path.join(bin, 'curl'), `#!/bin/sh
printf '%s\\n' "$*" >> "$FIXTURE/requests"
case "$*" in
  *releases/latest*) printf '%s' 'https://github.com/lykeion-tech/lykeion/releases/tag/v1.2.3';;
  *SHA256SUMS*) [ "\${FAIL_DOWNLOAD:-}" != checksums ] || exit 22; cp "$FIXTURE/SHA256SUMS" "$3";;
  *lyk-cli-1.2.3-*.tar.gz*) [ "\${FAIL_DOWNLOAD:-}" != archive ] || exit 22; cp "$FIXTURE/fixture.tar.gz" "$3";;
  *) echo "unexpected curl request: $*" >&2; exit 1;;
esac
`, { mode: 0o755 });
  return { dir, dest, bin, home, run(extra = {}) { return spawnSync('bash', [installer], { encoding: 'utf8', env: { ...process.env, HOME: home, PATH: `${bin}:${process.env.PATH}`, LYK_BIN_DIR: dest, FIXTURE: dir, TEST_OS: 'Linux', TEST_ARCH: 'x86_64', ...extra } }); }, cleanup() { rmSync(dir, { recursive: true, force: true }); } };
}

test('installs the matching CLI for each supported platform', () => {
  const f = fixture(); try {
    for (const [os, arch, expected] of [['Darwin', 'arm64', 'darwin-arm64'], ['Darwin', 'x86_64', 'darwin-amd64'], ['Linux', 'aarch64', 'linux-arm64'], ['Linux', 'x86_64', 'linux-amd64']]) {
      writeFileSync(path.join(f.dir, 'requests'), '');
      const run = f.run({ TEST_OS: os, TEST_ARCH: arch });
      assert.equal(run.status, 0, `${os}/${arch}: ${run.stderr}`);
      assert.ok(readFileSync(path.join(f.dir, 'requests'), 'utf8').includes(`lyk-cli-1.2.3-${expected}.tar.gz`));
      assert.equal(execFileSync(path.join(f.dest, 'lyk'), { encoding: 'utf8' }).trim(), 'installed');
    }
  } finally { f.cleanup(); }
});

test('failed downloads, wrong checksums and unsupported systems preserve the old CLI', () => {
  const f = fixture(); try {
    const target = path.join(f.dest, 'lyk'); writeFileSync(target, 'old CLI', { mode: 0o755 });
    for (const extra of [{ FAIL_DOWNLOAD: 'checksums' }, { FAIL_DOWNLOAD: 'archive' }, { TEST_OS: 'FreeBSD' }, { TEST_ARCH: 'i386' }]) {
      assert.notEqual(f.run(extra).status, 0);
      assert.equal(readFileSync(target, 'utf8'), 'old CLI');
    }
    writeFileSync(path.join(f.dir, 'fixture.tar.gz'), 'tampered');
    assert.notEqual(f.run().status, 0);
    assert.equal(readFileSync(target, 'utf8'), 'old CLI');
  } finally { f.cleanup(); }
});

test('a failed staging operation preserves the old CLI', () => {
  const f = fixture(); try {
    const target = path.join(f.dest, 'lyk'); writeFileSync(target, 'old CLI', { mode: 0o755 });
    writeFileSync(path.join(f.bin, 'install'), '#!/bin/sh\nexit 1\n', { mode: 0o755 });
    assert.notEqual(f.run().status, 0);
    assert.equal(readFileSync(target, 'utf8'), 'old CLI');
    assert.deepEqual(readdirSync(f.dest), ['lyk']);
  } finally { f.cleanup(); }
});

test('refuses an archive containing extra files', () => {
  const f = fixture(); try {
    writeFileSync(path.join(f.dir, 'extra'), 'bad');
    execFileSync('tar', ['-czf', path.join(f.dir, 'fixture.tar.gz'), '-C', f.dir, 'lyk', 'extra']);
    const digest = createHash('sha256').update(readFileSync(path.join(f.dir, 'fixture.tar.gz'))).digest('hex');
    writeFileSync(path.join(f.dir, 'SHA256SUMS'), `${digest}  lyk-cli-1.2.3-linux-amd64.tar.gz\n`);
    assert.notEqual(f.run().status, 0);
  } finally { f.cleanup(); }
});

function packageFixture(f) {
  const head = 'a'.repeat(40);
  writeFileSync(path.join(f.bin, 'git'), `#!/bin/sh
[ "$1" != -C ] || shift 2
case "$1" in
  status) [ "\${TEST_DIRTY:-}" != yes ] || echo '?? unexpected.go';;
  rev-parse) case "$*" in *refs/tags*) printf '%s\\n' "\${TEST_TAG:-${head}}";; *) printf '%s\\n' '${head}';; esac;;
  *) exit 1;;
esac
`, { mode: 0o755 });
  writeFileSync(path.join(f.bin, 'go'), `#!/bin/sh
case "$1" in
  build) shift; while [ "$1" != -o ]; do shift; done; shift; printf '%s/%s' "$GOOS" "$GOARCH" > "$1";;
  version) shift 2; target=$(cat "$1"); os=\${target%/*}; arch=\${target#*/}; [ "\${TEST_BAD_METADATA:-}" != yes ] || arch=wrong; printf 'build\\tGOOS=%s\\nbuild\\tGOARCH=%s\\nbuild\\tvcs.revision=%s\\nbuild\\tvcs.modified=%s\\n' "$os" "$arch" "\${TEST_BUILD_REVISION:-${head}}" "\${TEST_BUILD_MODIFIED:-false}";;
  *) exit 1;;
esac
`, { mode: 0o755 });
  const packager = path.resolve(path.dirname(fileURLToPath(import.meta.url)), 'package-cli.sh');
  return (output, extra = {}) => spawnSync('bash', [packager, '1.2.3', output], { encoding: 'utf8', env: { ...process.env, PATH: `${f.bin}:${process.env.PATH}`, ...extra } });
}

test('packages the four release targets and matching checksums', () => {
  const f = fixture(); try {
    const output = path.join(f.dir, 'out'); mkdirSync(output);
    const pack = packageFixture(f);
    const run = pack(output);
    assert.equal(run.status, 0, run.stderr);
    const sums = readFileSync(path.join(output, 'SHA256SUMS'), 'utf8');
    for (const [os, arch] of [['darwin', 'amd64'], ['darwin', 'arm64'], ['linux', 'amd64'], ['linux', 'arm64']]) {
      const name = `lyk-cli-1.2.3-${os}-${arch}.tar.gz`;
      assert.equal(execFileSync('tar', ['-xOzf', path.join(output, name), 'lyk'], { encoding: 'utf8' }), `${os}/${arch}`);
      const digest = createHash('sha256').update(readFileSync(path.join(output, name))).digest('hex');
      assert.ok(sums.includes(`${digest}  ${name}\n`));
    }
    assert.notEqual(pack(output).status, 0, 'existing release must not be overwritten');
  } finally { f.cleanup(); }
});

test('release packaging rejects dirty source, a different tag, and incorrect binary metadata', () => {
  const f = fixture(); try {
    const pack = packageFixture(f);
    for (const [i, extra] of [{ TEST_DIRTY: 'yes' }, { TEST_TAG: 'b'.repeat(40) }, { TEST_BAD_METADATA: 'yes' }, { TEST_BUILD_REVISION: 'b'.repeat(40) }, { TEST_BUILD_MODIFIED: 'true' }].entries()) {
      const output = path.join(f.dir, `rejected-${i}`);
      const result = pack(output, extra);
      assert.notEqual(result.status, 0, `unexpectedly accepted ${JSON.stringify(extra)}`);
      if (readdirSync(f.dir).includes(`rejected-${i}`)) assert.deepEqual(readdirSync(output), []);
    }
  } finally { f.cleanup(); }
});

test('warns when an earlier PATH entry will continue to run a different CLI', () => {
  const f = fixture(); try {
    writeFileSync(path.join(f.bin, 'lyk'), '#!/bin/sh\necho old\n', { mode: 0o755 });
    const run = f.run();
    assert.equal(run.status, 0, run.stderr);
    assert.match(run.stdout, /another lyk.*PATH/i);
    assert.equal(execFileSync(path.join(f.bin, 'lyk'), { encoding: 'utf8' }).trim(), 'old');
  } finally { f.cleanup(); }
});

test('refuses an existing directory or symlink to a directory', () => {
  const f = fixture(); try {
    const target = path.join(f.dest, 'lyk');
    mkdirSync(target);
    assert.notEqual(f.run().status, 0);
    assert.deepEqual(readdirSync(target), []);
    rmSync(target, { recursive: true });
    const other = path.join(f.dir, 'other'); mkdirSync(other);
    symlinkSync(other, target);
    assert.notEqual(f.run().status, 0);
    assert.deepEqual(readdirSync(other), []);
  } finally { f.cleanup(); }
});
