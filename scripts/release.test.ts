import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  archiveIntegrity,
  binaryContainsExactVersion,
  parseBuildMetadata,
	parseRemoteAnnotatedTag,
  shouldPublishPackage,
  validateTargets,
} from './release.ts';

test('requires the complete six-target release matrix', () => {
  assert.doesNotThrow(() =>
    validateTargets([
      'linux-x64',
      'linux-arm64',
      'darwin-x64',
      'darwin-arm64',
      'win32-x64',
      'win32-arm64',
    ]),
  );

  assert.throws(
    () => validateTargets(['linux-x64', 'linux-arm64']),
    /release archive must contain exactly 6 native targets/,
  );
});

test('finds only an exact printable embedded version', () => {
  const binary = Buffer.from(['prefix', 'dependency-v0.2.1', 'v0.2.1', '0.2.1', 'suffix'].join('\0'));

  assert.equal(binaryContainsExactVersion(binary, '0.2.1'), true);
  assert.equal(binaryContainsExactVersion(binary, '0.2.2'), false);
});

test('reads the target architecture from Go build metadata', () => {
  const metadata = parseBuildMetadata(`
package/moji: go1.25.0
\tpath\tgithub.com/microck/moji/cmd/moji
\tbuild\tCGO_ENABLED=0
\tbuild\tGOARCH=arm64
\tbuild\tGOOS=windows
`);

  assert.deepEqual(metadata, { goos: 'windows', goarch: 'arm64' });
});

test('resumes after npm publication only when the verified archive is identical', () => {
	const integrity = archiveIntegrity(Buffer.from('verified archive'));
	assert.equal(shouldPublishPackage(null, integrity), true);
	assert.equal(shouldPublishPackage(integrity, integrity), false);
	assert.throws(() => shouldPublishPackage(archiveIntegrity(Buffer.from('other archive')), integrity), /different archive bytes/);
});

test('resumes from a matching remote annotated tag and rejects unsafe tags', () => {
	const commit = '1111111111111111111111111111111111111111';
	const tagObject = '2222222222222222222222222222222222222222';
	assert.equal(parseRemoteAnnotatedTag('', 'v1.2.3', commit), 'missing');
	assert.equal(
		parseRemoteAnnotatedTag(
			`${tagObject}\trefs/tags/v1.2.3\n${commit}\trefs/tags/v1.2.3^{}\n`,
			'v1.2.3',
			commit,
		),
		'matching',
	);
	assert.throws(() => parseRemoteAnnotatedTag(`${commit}\trefs/tags/v1.2.3\n`, 'v1.2.3', commit), /not an annotated tag/);
	assert.throws(
		() => parseRemoteAnnotatedTag(`${tagObject}\trefs/tags/v1.2.3\n${tagObject}\trefs/tags/v1.2.3^{}\n`, 'v1.2.3', commit),
		/not current commit/,
	);
});
