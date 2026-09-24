import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import test from 'node:test';

const guide = readFileSync(new URL('../skills/octo-docs/ppt.md', import.meta.url), 'utf8');
const secret = 'https://cdn.example/media.svg?signature=TEST_ONLY_SECRET';
function exercise(result) {
  const match = guide.match(/### Signed-source URL handling[\s\S]*?```javascript\n([\s\S]*?)\n```/);
  assert.ok(match, 'missing executable signed-source example');
  const code = match[1].replace("import { spawnSync } from 'node:child_process';", '');
  const calls = [], output = [];
  const process = { env: { PPT_SOURCE_URL: secret, PPT_DOC_ID: 'deck', OCTO_CLI_PATH: '/trusted/octo-cli', OCTO_BOT_ID: 'bot' }, stdout: { write: text => output.push(text) } };
  runInNewContext(code, { process, spawnSync: (...args) => { calls.push(args); return result; } });
  assert.equal(calls.length, 1);
  const [file, args, opts] = calls[0];
  assert.equal(file, '/trusted/octo-cli');
  assert.deepEqual(Array.from(args), ['api', 'POST', '/v1/bot/docs/deck/attachments/ingest', '--no-retry', '--timeout', '90s', '--data', '@-']);
  assert.equal(opts.timeout, 120000);
  assert.ok(!JSON.stringify(args).includes(secret));
  assert.equal(JSON.parse(opts.input).urls[0], secret);
  assert.equal(opts.env.PPT_SOURCE_URL, undefined);
  assert.equal(opts.env.OCTO_BOT_ID, 'bot');
  assert.ok(!output.join('').includes(secret));
  return JSON.parse(output.join(''));
}

const mapping = { sourceUrl: secret, attachId: 'att_x', ref: 'ppt-media:att_x', mime: 'image/svg+xml', sizeBytes: 120 };
const resultFor = data => ({ status: 0, stdout: JSON.stringify({ ok: true, data }), stderr: '' });
for (const nested of [false, true]) test(`signed source remains off argv/output, nested=${nested}`, () => {
  const data = { mappings: [mapping], notIngested: [] };
  const actual = exercise({ status: 0, stdout: JSON.stringify({ ok: true, data: nested ? { data } : data }), stderr: '' });
  assert.deepEqual(actual, { receipts: [{ attachId: 'att_x', ref: 'ppt-media:att_x', mime: 'image/svg+xml', sizeBytes: 120 }], failedCount: 0, failures: [] });
});

for (const [name, data] of [
  ['empty result', { mappings: [], notIngested: [] }],
  ['duplicate successes', { mappings: [mapping, mapping], notIngested: [] }],
  ['success and failure', { mappings: [mapping], notIngested: [{ sourceUrl: secret, reason: 'fetch_failed' }] }],
  ...[{ mime: 'image/html' }, { sizeBytes: 0 }, { sizeBytes: -1 }, { sizeBytes: 1.5 },
    { sourceUrl: 'https://other.example/x' }, { sourceUrl: undefined }, { attachId: 123 }, { ref: 'https://other.example/x' }]
    .map(patch => [JSON.stringify(patch), { mappings: [{ ...mapping, ...patch }], notIngested: [] }]),
  ['null mapping', { mappings: [null], notIngested: [] }],
  ['null failure', { mappings: [], notIngested: [null] }],
  ['missing failure source', { mappings: [], notIngested: [{ reason: 'fetch_failed' }] }],
  ['mismatched failure source', { mappings: [], notIngested: [{ sourceUrl: 'https://other.example/x', reason: 'fetch_failed' }] }],
  ['missing failure reason', { mappings: [], notIngested: [{ sourceUrl: secret }] }],
]) test(`invalid single-source receipt is rejected: ${name}`, () => {
  assert.throws(() => exercise(resultFor(data)), e => /Invalid/.test(String(e)) && !String(e).includes(secret));
});

for (const reason of ['fetch_failed', 'size_too_large', secret]) test(`safe indexed failure: ${reason === secret ? 'unknown' : reason}`, () => {
  const actual = exercise(resultFor({ mappings: [], notIngested: [{ sourceUrl: secret, reason }] }));
  assert.deepEqual(actual, { receipts: [], failedCount: 1, failures: [{ index: 0, reason: reason === secret ? 'ingestion_failed' : reason }] });
});

for (const result of [
  { status: 1, stdout: secret, stderr: secret },
  { error: new Error(secret), status: null },
  { status: 0, stdout: secret },
  { status: 0, stdout: JSON.stringify({ ok: false, error: secret }) },
]) test(`failure diagnostics do not echo source URLs: ${JSON.stringify(result.status)}`, () => {
  assert.throws(() => exercise(result), e => !String(e).includes(secret) && /Ingestion|Invalid/.test(String(e)));
});
