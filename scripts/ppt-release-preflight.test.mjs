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

test('sharing response documentation names the stale-epoch conflict', () => {
  const spec = JSON.parse(readFileSync(new URL('../internal/registry/specs/docs.json', import.meta.url), 'utf8'));
  const response = spec.paths['/v1/bot/docs/{docId}/share'].put.responses['409'];
  assert.match(response.description, /share_settings_conflict/);
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

test('CI configuration re-includes tested Markdown after push exclusions', () => {
  // Configuration guard, not an execution of GitHub's event matcher.
  const source = readFileSync(new URL('../.github/workflows/ci.yml', import.meta.url), 'utf8');
  const push = source.split('  push:\n')[1]?.split('  pull_request:\n')[0] || '';
  assert.match(push, /\n    paths:\n/, 'push must re-include tested Markdown instead of ignoring all root Markdown');
  assert.ok(push.indexOf("- 'README.md'") > push.indexOf("- '!*.md'"), 'README push exception must follow exclusions');
  assert.ok(push.indexOf("- 'CHANGELOG.md'") > push.indexOf("- '!*.md'"), 'CHANGELOG push exception must follow exclusions');
  assert.ok(push.indexOf("- 'docs/ppt-release-gate.md'") > push.indexOf("- '!docs/**'"), 'release gate push exception must follow exclusions');
  assert.match(source, /tested_docs:\n\s+- '\{README\.md,CHANGELOG\.md,docs\/ppt-release-gate\.md\}'/, 'one brace pattern uses OR within the every-quantifier filter');
  const output = source.split('\n').find(line => line.trimStart().startsWith('code: ${{')) || '';
  assert.ok(output.includes("|| steps.filter.outputs.tested_docs == 'true'"), 'tested docs must enable the existing required build job');
  assert.match(source, /predicate-quantifier: 'every'/, 'preserve unrelated docs-only skip semantics');
});

function backend(alter = () => undefined) {
  const calls = [];
  const docs = new Map();
  let permissionEpoch = 4;
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
    if (url.endsWith('/share')) {
      const settings = () => ({ docId: id, shareScope: 'restricted', shareRole: 'read', permissionEpoch });
      if (options.method === 'PUT') {
        const body = JSON.parse(options.body);
        assert.deepEqual(Object.keys(body).sort(), ['permissionEpoch', 'shareScope']);
        assert.equal(body.shareScope, 'restricted', 'never broaden access');
        if (body.permissionEpoch !== permissionEpoch) {
          return json({ error: 'share_settings_conflict', current: settings() }, 409);
        }
        permissionEpoch++;
      }
      return json(settings());
    }
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
  assert.equal(server.calls.length, 28);
  assert.deepEqual(posts.slice(0, 5).map(call => JSON.parse(call.body).templateId), ids);
  assert.equal(new Set(posts.map(call => call.headers['Idempotency-Key'])).size, 10);
  assert.match(logs.join('\n'), /d_0/);
  assert.ok(!logs.join('\n').includes(env.PPT_RELEASE_BOT_TOKEN));
});

test('missing/unsafe configuration fails before any request', async t => {
  for (const config of [
    {}, { ...env, PPT_RELEASE_BOT_TOKEN: '' }, { ...env, PPT_RELEASE_BOT_TOKEN: 'uk_human' },
    { ...env, PPT_RELEASE_SPACE_ID: '' },
    ...['http://octo.example', 'https://u:p@octo.example', 'https://octo.example/api', 'https://octo.example/octo',
      'https://octo.example/?x=1', 'https://octo.example/#x'].map(PPT_RELEASE_API_ORIGIN => ({ ...env, PPT_RELEASE_API_ORIGIN })),
  ]) {
    await t.test(JSON.stringify({ ...config, PPT_RELEASE_BOT_TOKEN: '<redacted>' }), async () => {
      await assert.rejects(preflight({ env: config, fetch: () => assert.fail('unsafe request'), log: () => {} }), /configuration/);
    });
  }
});

test('each fresh template requires at least one slide, including blank', async t => {
  // These fixtures verify the probe's minimum contract, not template assets
  // or deployment readiness. Blank is one empty slide, not zero slides.
  for (const [index, templateId] of ids.entries()) {
    for (const slides of [[{ id: 'initial', children: [] }], []]) {
      await t.test(`${templateId}: ${slides.length} slides`, async () => {
        const readStep = 2 * index + 2;
        const server = backend((url, _options, count) => count === readStep && json({ data: {
          docId: new URL(url).pathname.split('/').at(-2), baseRevision: 0,
          deck: { format: 'bento/slides', version: 1, slides },
        } }));
        const run = preflight({ env, fetch: server.fetch, log: () => {} });
        if (slides.length) {
          await run;
          assert.equal(server.calls.length, 14);
        } else {
          await assert.rejects(run, new RegExp(`${templateId} readback invalid`));
          assert.equal(server.calls.length, readStep, 'stop at the empty template, without retry');
        }
      });
    }
  }
});

test('bundled gallery-sized decks are accepted within the response bound', async () => {
  // The bundled Orbital JSON is about 3.54 MB (embedded assets/fonts included).
  const server = backend((url, options) => url.endsWith('/ppt') && options.method === 'GET' && json({ data: {
    docId: new URL(url).pathname.split('/').at(-2), baseRevision: 0,
    deck: { format: 'bento/slides', version: 1, slides: [{ id: 'slide' }], asset: 'x'.repeat(4 * 1024 * 1024) },
  } }));
  await preflight({ env, fetch: server.fetch, log: () => {} });
  assert.equal(server.calls.length, 14);
});

test('probes sharing on a newly created retained document with a stale-write check and readback', async () => {
  const server = backend();
  await preflight({ env, fetch: server.fetch, log: () => {} });
  const calls = server.calls.filter(call => call.url.endsWith('/share'));
  assert.deepEqual(calls.map(call => call.method), ['GET', 'PUT', 'PUT', 'GET']);
  assert.ok(calls.every(call => call.url === 'https://octo.example/v1/bot/docs/d_0/share'));
  assert.deepEqual(JSON.parse(calls[1].body), { shareScope: 'restricted', permissionEpoch: 4 });
  assert.equal(calls[1].body, calls[2].body, 'only the deliberate stale-write check repeats the original epoch');
});

test('sharing contract failures block publication without retry or fallback', async t => {
  const settings = { docId: 'd_0', shareScope: 'restricted', shareRole: 'read', permissionEpoch: 4 };
  for (const [name, step, response, expected] of [
    ['missing epoch', 1, json({ ...settings, permissionEpoch: undefined }), /share read invalid/],
    ['unsafe epoch', 1, json({ ...settings, permissionEpoch: Number.MAX_SAFE_INTEGER + 1 }), /share read invalid/],
    ['wrong document', 1, json({ ...settings, docId: 'd_other' }), /share read invalid/],
    ['nonrestricted initial scope', 1, json({ ...settings, shareScope: 'anyone_in_space' }), /share read invalid/],
    ['no epoch advancement', 2, json(settings), /share update invalid/],
    ['write broadens access', 2, json({ ...settings, permissionEpoch: 5, shareScope: 'anyone_in_space' }), /share update invalid/],
    ['write unauthorized', 2, json({}, 403), /HTTP status 403/],
    ['stale write accepted', 3, json({ ...settings, permissionEpoch: 6 }), /HTTP status 200/],
    ['unrelated conflict', 3, json({ error: 'other_conflict' }, 409), /share conflict invalid/],
    ['readback changed', 4, json({ ...settings, permissionEpoch: 6 }), /share readback invalid/],
  ]) {
    await t.test(name, async () => {
      let shares = 0;
      const server = backend(url => url.endsWith('/share') && ++shares === step && response);
      await assert.rejects(preflight({ env, fetch: server.fetch, log: () => {} }), expected);
      assert.equal(shares, step, 'stop at the failed check');
    });
  }
});

test('probe diagnostics distinguish safe failure categories without leaking server content', async t => {
  for (const [name, response, expected] of [
    ['status', () => json({ error: env.PPT_RELEASE_BOT_TOKEN }, 401), /HTTP status 401/],
    ['redirect', () => new Response('', { status: 302 }), /HTTP status 302/],
    ['content type', () => new Response('secret', { status: 201 }), /content type/],
    ['parser', () => new Response('{secret', { status: 201, headers: { 'content-type': 'application/json' } }), /invalid JSON/],
    ['size', () => json('x'.repeat(8 * 1024 * 1024), 201), /response size limit/],
    ['transport', () => { throw new Error(env.PPT_RELEASE_API_ORIGIN + env.PPT_RELEASE_BOT_TOKEN); }, /transport/],
  ]) {
    await t.test(name, async () => {
      let calls = 0;
      await assert.rejects(preflight({ env, fetch: async () => { calls++; return response(); }, log: () => {} }), error => {
        assert.match(error.message, expected);
        for (const secret of ['secret', env.PPT_RELEASE_BOT_TOKEN, env.PPT_RELEASE_API_ORIGIN]) assert.ok(!error.message.includes(secret));
        return true;
      });
      assert.equal(calls, 1);
    });
  }
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
