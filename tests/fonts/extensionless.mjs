import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { createServer } from 'node:http';
import { readFile, readdir } from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';

const root = fileURLToPath(new URL('../../', import.meta.url));
const font = Buffer.from(await readFile(join(root, 'internal/fontconvert/testdata/test-woff2.base64'), 'utf8'), 'base64');
assert.equal(font.subarray(0, 4).toString(), 'wOF2');
const requireDocs = createRequire(join(root, 'docs/package.json'));
const requests = [];
const server = createServer((request, response) => {
  requests.push(request.url);
  response.writeHead(200, { 'Content-Type': 'font/woff2' });
  response.end(font);
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
try {
  const child = spawn(process.execPath, [requireDocs.resolve('next/dist/bin/next'), 'build'], {
    cwd: join(root, 'docs'),
    env: {
      ...process.env,
      MOJI_FONT_REGRESSION: '1',
      NEXT_FONT_GOOGLE_MOCKED_RESPONSES: join(root, 'tests/fonts/google-responses.cjs'),
      MOJI_TEST_FONT_ORIGIN: `http://127.0.0.1:${server.address().port}`,
    },
    stdio: 'inherit',
    timeout: 120_000,
  });
  const code = await new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('exit', resolve);
  });
  assert.equal(code, 0, 'production build with extensionless font response must pass');
  assert.ok(requests.includes('/l/font?kit=a%2Bb%25c=d&skey=test&v=v20'), 'query must survive intact');
  const media = join(root, 'docs/.next-font-regression/static/media');
  const files = await readdir(media);
  const emitted = await Promise.all(files.filter(name => name.endsWith('.woff2')).map(name => readFile(join(media, name))));
  assert.ok(emitted.some(bytes => bytes.equals(font)), 'build must self-host the exact fixture font');
  console.log('Extensionless font regression passed: intact query and emitted WOFF2.');
} finally {
  await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
}
