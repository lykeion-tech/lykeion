import { build } from 'esbuild';
import { lstat, mkdtemp, open, readFile, realpath, rename, rm, writeFile } from 'node:fs/promises';
import { constants } from 'node:fs';
import { builtinModules } from 'node:module';
import path from 'node:path';
import ts from 'typescript';

const allowed = new Set(['electron', ...builtinModules, ...builtinModules.map((name) => `node:${name}`)]);
allowed.delete('module');
allowed.delete('node:module');

export async function readPackageFile(directory, name) {
  if (!/^[A-Za-z0-9._-]+$/.test(name) || typeof constants.O_NOFOLLOW !== 'number')
    throw new Error('Invalid updater dependency file');
  const parent = await realpath(directory);
  const filename = path.join(parent, name);
  const handle = await open(filename, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    const opened = await handle.stat({ bigint: true });
    const named = await lstat(filename, { bigint: true });
    if (!opened.isFile() || !named.isFile() || named.isSymbolicLink() || opened.nlink !== 1n || opened.dev !== named.dev || opened.ino !== named.ino || opened.size > 1048576n || path.dirname(await realpath(filename)) !== parent)
      throw new Error('Invalid updater dependency file');
    const buffer = Buffer.alloc(1024 * 1024 + 1);
    let length = 0;
    while (length < buffer.length) {
      const { bytesRead } = await handle.read(buffer, length, buffer.length - length, null);
      if (bytesRead === 0) break;
      length += bytesRead;
    }
    const after = await handle.stat({ bigint: true });
    if (length > 1024 * 1024 || BigInt(length) !== opened.size || after.size !== opened.size || after.mtimeNs !== opened.mtimeNs || after.ctimeNs !== opened.ctimeNs)
      throw new Error('Invalid updater dependency file');
    return buffer.subarray(0, length).toString('utf8');
  } finally { await handle.close(); }
}

export function auditBundleSource(source) {
  const tree = ts.createSourceFile('updater.cjs', source, ts.ScriptTarget.ESNext, true, ts.ScriptKind.JS);
  if (tree.parseDiagnostics.length) throw new Error('Invalid updater bundle');
  const visit = (node) => {
    if (ts.isIdentifier(node) && node.text === 'eval') throw new Error('Unexpected updater module load');
    if (ts.isIdentifier(node) && node.text === 'Function' &&
        !(ts.isPropertyAccessExpression(node.parent) && node.parent.expression === node && node.parent.name.text === 'prototype'))
      throw new Error('Unexpected updater module load');
    if (ts.isIdentifier(node) && node.text === 'require' &&
        !(ts.isCallExpression(node.parent) && node.parent.expression === node))
      throw new Error('Unexpected updater module load');
    if (ts.isIdentifier(node) && node.text === 'module' && !ts.isParameter(node.parent) &&
        !(ts.isPropertyAccessExpression(node.parent) && node.parent.expression === node && node.parent.name.text === 'exports'))
      throw new Error('Unexpected updater module load');
    if (ts.isElementAccessExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 'module')
      throw new Error('Unexpected updater module load');
    if (ts.isPropertyAccessExpression(node) && ['require', 'createRequire'].includes(node.name.text))
      throw new Error('Unexpected updater module load');
    if (ts.isElementAccessExpression(node) && ts.isStringLiteral(node.argumentExpression) && ['require', 'createRequire'].includes(node.argumentExpression.text))
      throw new Error('Unexpected updater module load');
    if (ts.isCallExpression(node)) {
      if (node.expression.kind === ts.SyntaxKind.ImportKeyword) throw new Error('Unexpected updater module load');
      if (ts.isIdentifier(node.expression) && node.expression.text === 'require') {
        if (node.arguments.length !== 1 || !ts.isStringLiteral(node.arguments[0]) || !allowed.has(node.arguments[0].text))
          throw new Error('Unexpected updater module load');
      }
      if (ts.isPropertyAccessExpression(node.expression) &&
          ((ts.isIdentifier(node.expression.expression) && node.expression.expression.text === 'module' && node.expression.name.text === 'require') ||
           (ts.isIdentifier(node.expression.expression) && node.expression.expression.text === 'require')))
        throw new Error('Unexpected updater module load');
    }
    ts.forEachChild(node, visit);
  };
  visit(tree);
}

export async function buildDesktopUpdater(root, output) {
  root = await realpath(root);
  const entry = path.join(root, 'node_modules/electron-updater/out/main.js');
  const result = await build({
    entryPoints: [entry], outfile: output, bundle: true, platform: 'node', format: 'cjs',
    target: 'node22', external: ['electron'], logLevel: 'silent', metafile: true,
    absWorkingDir: root, write: false,
  });
  for (const file of Object.values(result.metafile.outputs))
    for (const item of file.imports)
      if (item.external && !allowed.has(item.path)) throw new Error('Unexpected updater external dependency');
  const moduleRoot = await realpath(path.join(root, 'node_modules'));
  const packages = new Map();
  for (const input of Object.keys(result.metafile.inputs)) {
    const absolute = await realpath(path.resolve(root, input));
    if (!absolute.startsWith(`${moduleRoot}${path.sep}`)) throw new Error('Unexpected updater source');
    let directory = path.dirname(absolute);
    while (directory.startsWith(`${moduleRoot}${path.sep}`)) {
      try {
        const pkg = JSON.parse(await readPackageFile(directory, 'package.json'));
        if (typeof pkg.name !== 'string' || typeof pkg.version !== 'string') throw new Error();
        packages.set(directory, pkg);
        break;
      } catch (error) {
        if (error?.code !== 'ENOENT') throw new Error('Invalid updater dependency metadata');
      }
      directory = path.dirname(directory);
    }
    if (!packages.has(directory)) throw new Error('Missing updater dependency metadata');
  }
  const notices = [];
  for (const [directory, pkg] of [...packages].sort((a, b) => a[1].name.localeCompare(b[1].name) || a[1].version.localeCompare(b[1].version))) {
    let license;
    for (const name of ['LICENSE', 'LICENSE.md', 'license', 'license.md', 'LICENCE']) {
      try { license = await readPackageFile(directory, name); break; }
      catch (error) { if (error?.code !== 'ENOENT') throw error; }
    }
    // The npm package contains no license file; retain its published metadata and MIT text.
    if (!license && pkg.name === 'lazy-val' && pkg.version === '1.0.5' && pkg.license === 'MIT') {
      license = `Author: ${pkg.author}\nThe npm package declares the MIT license but includes no license file.\n\n${await readPackageFile(path.join(root, 'scripts'), 'updater-mit-license.txt')}`;
    }
    if (!license) throw new Error(`Missing license for updater dependency ${pkg.name}`);
    notices.push(`${pkg.name}@${pkg.version}\n${license.trim()}\n`);
  }
  if (result.outputFiles.length !== 1) throw new Error('Unexpected updater bundle output');
  const sourceText = result.outputFiles[0].text;
  if (sourceText.split('Function("return this")()').length !== 3)
    throw new Error('Unexpected updater global fallback');
  const comment = notices.join('\n---\n\n').split(/\r\n|\r|\n|\u2028|\u2029/).map((line) => `// ${line}`).join('\n');
  const bundle = `${sourceText.replaceAll('Function("return this")()', 'globalThis')}\n// Third-party notices\n${comment}\n`;
  auditBundleSource(bundle);
  try {
    const info = await lstat(output);
    if (!info.isFile() || info.isSymbolicLink()) throw new Error('Invalid updater output');
  } catch (error) {
    if (error?.code !== 'ENOENT') throw error;
  }
  const stage = await mkdtemp(path.join(path.dirname(output), '.updater-build-'));
  const staged = path.join(stage, 'bundle');
  try {
    await writeFile(staged, bundle, { flag: 'wx', mode: 0o600 });
    await rename(staged, output);
  } finally {
    await rm(stage, { recursive: true, force: true });
  }
}
