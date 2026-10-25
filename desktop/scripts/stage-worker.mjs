import { access, chmod, copyFile, mkdir } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import path from 'node:path';

const suffix = process.platform === 'win32' ? '.exe' : '';
const osName = process.platform === 'win32' ? 'windows' : process.platform;
const arch = process.arch === 'x64' ? 'amd64' : process.arch;
const source = process.env.PDFSEARCH_WORKER || path.resolve(`../dist/pdfsearch-${osName}-${arch}${suffix}`);
await access(source);
// Check that the sidecar is bundled for the target and includes the persistent
// worker command. Packing an older CLI would otherwise fail on first launch.
const info = JSON.parse(execFileSync(source, ['bundle-info'], { encoding: 'utf8' }));
const help = execFileSync(source, ['--help'], { encoding: 'utf8' });
if (!help.includes('pdfsearch worker') || info.os !== osName || info.arch !== arch) throw new Error('Build the matching bundled Go worker before packaging.');
await mkdir('resources/worker', { recursive: true });
await copyFile(source, `resources/worker/pdfsearch${suffix}`);
if (process.platform !== 'win32') await chmod(`resources/worker/pdfsearch${suffix}`, 0o755);
console.log(`Staged bundled worker (${info.os}/${info.arch}, glibc ${info.minimum_glibc || 'n/a'}).`);
