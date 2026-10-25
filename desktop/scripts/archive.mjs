import { execFileSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import path from 'node:path';

// Use the platform tar/gzip implementation rather than a second concurrent
// packager archiver. This also provides a non-FUSE alternative to AppImage.
const { version } = JSON.parse(await readFile('package.json', 'utf8'));
const destination = path.resolve(`releases/PassagePin-${version}-${process.arch}.tar.gz`);
execFileSync('tar', ['-czf', destination, '-C', path.resolve('releases'), 'linux-unpacked'], { stdio: 'inherit' });
console.log(`Created ${destination}`);
