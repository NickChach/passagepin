import { spawnSync } from 'node:child_process';
import { mkdtemp, readdir, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const desktop = fileURLToPath(new URL('..', import.meta.url));
const root = path.dirname(desktop);
const [mode, ...args] = process.argv.slice(2);
if (!['integration', 'e2e', 'verify'].includes(mode) || (mode !== 'e2e' && args.length)) {
  throw new Error('Usage: node scripts/test.mjs integration|e2e|verify [Playwright options for e2e]');
}

function run(command, arguments_, cwd, env = process.env) {
  const result = spawnSync(command, arguments_, { cwd, env, stdio: 'inherit' });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    process.exitCode = result.status || 1;
    throw new Error(`${command} failed${result.signal ? ` (${result.signal})` : ''}.`);
  }
}

const work = await mkdtemp(path.join(os.tmpdir(), 'passagepin-tests-'));
try {
  const binary = path.join(work, 'pdfsearch');
  console.log('Building the current Go worker for desktop tests…');
  run('go', ['run', '-buildvcs=false', './cmd/bundlebuild',
    '--model-dir', process.env.PDFSEARCH_MODEL_DIR || path.join(root, '.models'),
    '--runtime-dir', process.env.PDFSEARCH_TEST_RUNTIME_DIR || path.join(root, '.runtime/onnxruntime-linux-x64-1.22.0'),
    '--out', binary], root);

  // Always test this build, even if a development worker override was exported.
  // Use its bundled assets and an isolated native cache.
  const env = { ...process.env, PDFSEARCH_WORKER: binary, XDG_CACHE_HOME: path.join(work, 'cache'),
    PDFSEARCH_MODEL_DIR: '', ONNXRUNTIME_SHARED_LIBRARY_PATH: '' };
  if (mode !== 'integration') run('npm', ['run', 'build'], desktop, env);
  if (mode !== 'e2e') {
    const files = (await readdir(path.join(desktop, 'tests')))
      .filter(name => name.endsWith('.test.ts') || name.endsWith('.integration.ts'))
      .sort().map(name => path.join('tests', name));
    run(process.execPath, ['--import', 'tsx', '--test', ...files], desktop, env);
  }
  if (mode !== 'integration') {
    run(process.execPath, ['node_modules/@playwright/test/cli.js', 'test', ...args], desktop, env);
  }
} catch (error) {
  console.error(error.message);
  process.exitCode ||= 1;
} finally {
  await rm(work, { recursive: true, force: true });
}
