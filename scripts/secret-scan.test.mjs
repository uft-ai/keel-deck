import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { historyArgs, run } from './secret-scan.mjs';

test('product history includes both merge parents while the all-ref audit retains backups', (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'secret-scan-history-'));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const git = (...args) => execFileSync('git', args, { cwd: directory, encoding: 'utf8' }).trim();
  git('init', '-q');
  git('config', 'user.name', 'Fixture');
  git('config', 'user.email', 'fixture@example.invalid');
  git('commit', '-q', '--allow-empty', '-m', 'root');
  const root = git('rev-parse', 'HEAD');
  const tree = git('rev-parse', 'HEAD^{tree}');
  const product = git('commit-tree', tree, '-p', root, '-m', 'product');
  const upstream = git('commit-tree', tree, '-p', root, '-m', 'upstream');
  const backup = git('commit-tree', tree, '-p', root, '-m', 'backup');
  const merged = git('commit-tree', tree, '-p', product, '-p', upstream, '-m', 'merge');
  git('update-ref', 'HEAD', merged);
  git('update-ref', 'refs/backups/old-product', backup);

  for (const [mode, expected] of [
    ['--history', [root, product, upstream, merged]],
    ['--all', [root, product, upstream, merged, backup]],
  ]) {
    const args = historyArgs(mode);
    const logOptions = args.filter((arg) => arg.startsWith('--log-opts='));
    assert.equal(logOptions.length, 1);
    const revision = logOptions[0].slice('--log-opts='.length);
    // Exercise Git's ancestry selection, including a commit reachable only by a backup ref.
    assert.deepEqual(new Set(git('rev-list', revision).split('\n')), new Set(expected));
    assert.ok(args.includes('--redact=100'));
    assert.ok(args.includes('--config'));
  }

  const priorExitCode = process.exitCode;
  try {
    assert.equal(run('git', ['merge-base', '--is-ancestor', backup, merged], { cwd: directory, stdio: 'pipe' }), false);
    assert.equal(process.exitCode, 1, 'a nonzero scanner status must reach the caller');
    assert.equal(run('git', ['merge-base', '--is-ancestor', product, merged], { cwd: directory, stdio: 'pipe' }), true);
    assert.equal(process.exitCode, 1, 'a later success must not clear a failure');
  } finally {
    process.exitCode = priorExitCode;
  }
});
