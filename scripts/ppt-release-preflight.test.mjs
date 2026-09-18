import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { preflight } from './ppt-release-preflight.mjs';

const env = {
  PPT_RELEASE_API_ORIGIN: 'https://octo.example',
  PPT_RELEASE_BOT_TOKEN: 'app_test-secret',
  PPT_RELEASE_SPACE_ID: 'acceptance-space',
};
const ids = ['blank', 'signal', 'terra', 'orbital', 'picnic'];
const json = (value, status = 200) => new Response(JSON.stringify(value), {
  status, headers: { 'content-type': 'application/json' },
});

test('PPT create response documents the fields consumed by the release probe', () => {
  const spec = JSON.parse(readFileSync(new URL('../internal/registry/specs/docs.json', import.meta.url), 'utf8'));
  const create = spec.paths['/v1/bot/docs'].post;
  const schema = create.responses['201'].content['application/json'].schema;
  for (const field of ['docId', 'docType', 'templateId', 'title', 'spaceId']) {
    assert.equal(schema.properties[field]?.type, 'string', `create response must document ${field}`);
  }
  for (const field of ['draftRevision', 'snapshotVersion']) {
    assert.equal(schema.properties[field]?.type, 'integer', `create response must document ${field}`);
  }
  assert.match(schema.properties.docId.description || '', /d_.*24.*hex/);
  assert.equal(create['x-octo-response-unwrap'], undefined, 'create has a flat wire receipt');
  assert.equal(create['x-octo-strict-response-schema'], undefined, 'do not tighten ordinary doc responses');
  assert.equal(schema.required, undefined, 'PPT-only metadata must not become required for other doc types');
  assert.equal(schema.properties.templateId.enum, undefined, 'historical receipt template IDs stay unconstrained');
  assert.match(schema.example?.docId || '', /^d_[a-f0-9]{24}$/);
  assert.equal(schema.example.docType, 'html_ppt');
  assert.equal(schema.example.templateId, 'blank');
  for (const [field, value] of Object.entries(schema.example)) {
    const type = schema.properties[field]?.type;
    assert.ok(type, `example field ${field} must be in the response schema`);
    assert.equal(type === 'integer' ? Number.isInteger(value) : typeof value === type, true, field);
  }
});

test('README and CHANGELOG edits schedule their guards on PRs and main pushes', () => {
  const source = readFileSync(new URL('../.github/workflows/ci.yml', import.meta.url), 'utf8');
  const push = source.split('  push:\n')[1]?.split('  pull_request:\n')[0] || '';
  assert.match(push, /\n    paths:\n/, 'push must re-include tested Markdown instead of ignoring all root Markdown');
  assert.ok(push.indexOf("- 'README.md'") > push.indexOf("- '!*.md'"), 'README push exception must follow exclusions');
  assert.ok(push.indexOf("- 'CHANGELOG.md'") > push.indexOf("- '!*.md'"), 'CHANGELOG push exception must follow exclusions');
  assert.match(source, /tested_docs:\n\s+- '\{README,CHANGELOG\}\.md'/, 'one brace pattern uses OR within the every-quantifier filter');
  const output = source.split('\n').find(line => line.trimStart().startsWith('code: ${{')) || '';
  assert.ok(output.includes("|| steps.filter.outputs.tested_docs == 'true'"), 'tested docs must enable the existing required build job');
  assert.match(source, /predicate-quantifier: 'every'/, 'preserve unrelated docs-only skip semantics');
});

function backend(alter = () => undefined) {
  const calls = [];
  const docs = new Map();
  const fetch = async (url, options) => {
    calls.push({ url, ...options });
    assert.equal(options.redirect, 'error');
    assert.equal(options.headers.Authorization, 'Bearer app_test-secret');
    assert.equal(options.headers['X-Space-Id'], undefined, 'Docs derives Space from the Bot, not a client header');
    assert.ok(options.signal);
    const override = alter(url, options, calls.length);
    if (override) return override;
    if (options.method === 'POST') {
      const spec = JSON.parse(readFileSync(new URL('../internal/registry/specs/docs.json', import.meta.url), 'utf8'));
      const [path] = Object.entries(spec.paths).find(([, methods]) => methods.post?.operationId === 'docs.create');
      assert.equal(url, `https://octo.example${path}`, 'release probe must use the CLI gateway route');
      const body = JSON.parse(options.body);
      const example = spec.paths[path].post.responses['201'].content['application/json'].schema.example;
      assert.ok(example, 'mock must use the documented source-backed PPT receipt');
      const receipt = { ...example, ...body, docId: `d_${docs.size}`, spaceId: env.PPT_RELEASE_SPACE_ID };
      docs.set(receipt.docId, receipt);
      return json(receipt, 201);
    }
    const id = new URL(url).pathname.split('/').at(-2);
    assert.ok(docs.has(id));
    return json({ data: { docId: id, baseRevision: 0, deck: {
      format: 'bento/slides', version: 1, slides: [{ id: 'slide' }],
    } } });
  };
  return { calls, fetch };
}

test('creates and reads all five templates using fresh keys on every invocation', async () => {
  const server = backend();
  const logs = [];
  const options = { env, fetch: server.fetch, log: line => logs.push(line) };
  await preflight(options);
  await preflight(options);
  const posts = server.calls.filter(call => call.method === 'POST');
  assert.equal(server.calls.length, 20);
  assert.deepEqual(posts.slice(0, 5).map(call => JSON.parse(call.body).templateId), ids);
  assert.equal(new Set(posts.map(call => call.headers['Idempotency-Key'])).size, 10);
  assert.match(logs.join('\n'), /d_0/);
  assert.ok(!logs.join('\n').includes(env.PPT_RELEASE_BOT_TOKEN));
});

test('missing/unsafe configuration fails before any request', async t => {
  for (const config of [
    {}, { ...env, PPT_RELEASE_BOT_TOKEN: '' }, { ...env, PPT_RELEASE_BOT_TOKEN: 'uk_human' },
    { ...env, PPT_RELEASE_SPACE_ID: '' },
    ...['http://octo.example', 'https://u:p@octo.example', 'https://octo.example/api',
      'https://octo.example/?x=1', 'https://octo.example/#x'].map(PPT_RELEASE_API_ORIGIN => ({ ...env, PPT_RELEASE_API_ORIGIN })),
  ]) {
    await t.test(JSON.stringify({ ...config, PPT_RELEASE_BOT_TOKEN: '<redacted>' }), async () => {
      await assert.rejects(preflight({ env: config, fetch: () => assert.fail('unsafe request'), log: () => {} }), /configuration/);
    });
  }
});

test('bundled gallery-sized decks are accepted within the response bound', async () => {
  // The bundled Orbital JSON is about 3.54 MB (embedded assets/fonts included).
  const server = backend((url, options) => options.method === 'GET' && json({ data: {
    docId: new URL(url).pathname.split('/').at(-2), baseRevision: 0,
    deck: { format: 'bento/slides', version: 1, slides: [{ id: 'slide' }], asset: 'x'.repeat(4 * 1024 * 1024) },
  } }));
  await preflight({ env, fetch: server.fetch, log: () => {} });
  assert.equal(server.calls.length, 10);
});

test('old backend, auth errors, redirects and malformed responses stop the gate', async t => {
  for (const [name, response] of [
    ['old backend', () => json({ error: 'unsupported template' }, 400)],
    ['auth', () => json({ error: 'unauthorized' }, 401)],
    ['redirect', () => new Response('', { status: 302, headers: { location: 'https://elsewhere.example' } })],
    // These are injected on a POST: preserve 201 so the status gate cannot
    // mask the content-type, JSON parser, or streaming-size guard under test.
    ['html', options => new Response(JSON.stringify({ ...JSON.parse(options.body), docId: 'd_probe', spaceId: env.PPT_RELEASE_SPACE_ID }),
      { status: 201, headers: { 'content-type': 'text/html' } })],
    ['json syntax', () => new Response('{', { status: 201, headers: { 'content-type': 'application/json' } })],
    ['oversized', options => json({ ...JSON.parse(options.body), docId: 'd_probe', spaceId: env.PPT_RELEASE_SPACE_ID,
      padding: 'x'.repeat(8 * 1024 * 1024) }, 201)],
    // All other receipt fields are valid. Only the echoed template differs,
    // modelling a silent fallback to blank rather than another missing field.
    ['wrong template', options => json({ ...JSON.parse(options.body), docId: 'd_x',
      spaceId: env.PPT_RELEASE_SPACE_ID, templateId: 'blank' }, 201)],
  ]) {
    await t.test(name, async () => {
      const server = backend((_url, options, count) => count === 3 && response(options));
      await assert.rejects(preflight({ env, fetch: server.fetch, log: () => {} }),
        name === 'wrong template' ? /signal create receipt mismatch/ : undefined);
      assert.equal(server.calls.length, 3, 'must not retry or continue after failure');
    });
  }
});

test('unusable deck and mismatched receipt cannot pass', async t => {
  for (const data of [ {}, { docId: 'other', baseRevision: 0, deck: { slides: [{}] } },
    { docId: 'd_0', baseRevision: 0, deck: { format: 'bento/slides', version: 1, slides: [] } },
  ]) {
    await t.test(JSON.stringify(data), async () => {
      const server = backend((_url, options) => options.method === 'GET' && json({ data }));
      await assert.rejects(preflight({ env, fetch: server.fetch, log: () => {} }), /readback/);
      assert.equal(server.calls.length, 2);
    });
  }
});

test('transport failures never leak credentials or retry ambiguous creates', async () => {
  let calls = 0;
  await assert.rejects(preflight({ env, log: () => {}, fetch: async () => {
    calls++;
    throw new Error(env.PPT_RELEASE_BOT_TOKEN);
  } }), error => !error.message.includes(env.PPT_RELEASE_BOT_TOKEN));
  assert.equal(calls, 1);
});

test('both publishing entry points depend on the gate, even with from_artifact', () => {
  for (const file of ['release-publish.yml', 'npm-publish.yml']) {
    const source = readFileSync(new URL(`../.github/workflows/${file}`, import.meta.url), 'utf8');
    assert.match(source, /publish:\n\s+needs: ppt-preflight/, `${file} can publish without preflight`);
    assert.match(source, /uses: \.\/\.github\/workflows\/ppt-release-preflight.yml/);
  }
});
