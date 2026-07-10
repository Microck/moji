#!/usr/bin/env node

import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const supportedArchitectures = new Set(['x64', 'arm64']);
const supportedPlatforms = new Set(['linux', 'darwin', 'win32']);

if (!supportedPlatforms.has(process.platform) || !supportedArchitectures.has(process.arch)) {
  console.error(
    `moji: this package does not include a binary for ${process.platform}-${process.arch}. ` +
      'Use Linux, macOS, or Windows on x64 or arm64, or build Moji from source with Go.',
  );
  process.exit(1);
}

const packageRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const executable = process.platform === 'win32' ? 'moji.exe' : 'moji';
const binary = resolve(packageRoot, 'binaries', `${process.platform}-${process.arch}`, executable);

if (!existsSync(binary)) {
  console.error(
    `moji: the bundled ${process.platform}-${process.arch} binary is missing. ` +
      'Reinstall @microck/moji. If reinstalling does not help, report the package version and platform.',
  );
  process.exit(1);
}

const child = spawnSync(binary, process.argv.slice(2), { stdio: 'inherit' });

if (child.error) {
  console.error(`moji: the bundled binary could not start: ${child.error.message}. Reinstall @microck/moji and try again.`);
  process.exit(1);
}

process.exit(child.status ?? 1);
