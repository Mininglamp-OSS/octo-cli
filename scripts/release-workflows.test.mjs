import assert from 'node:assert/strict';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import test from 'node:test';

const read = path => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8');

test('sharing response documentation names the stale-epoch conflict', () => {
  const spec = JSON.parse(read('internal/registry/specs/docs.json'));
  assert.match(spec.paths['/v1/bot/docs/{docId}/share'].put.responses['409'].description, /share_settings_conflict/);
});

test('PPT create response preserves the flat, backwards-compatible receipt contract', () => {
  const spec = JSON.parse(read('internal/registry/specs/docs.json'));
  const create = spec.paths['/v1/bot/docs'].post;
  const schema = create.responses['201'].content['application/json'].schema;
  for (const field of ['docId', 'docType', 'templateId', 'title', 'spaceId']) {
    assert.equal(schema.properties[field]?.type, 'string', field);
  }
  for (const field of ['draftRevision', 'snapshotVersion']) {
    assert.equal(schema.properties[field]?.type, 'integer', field);
  }
  assert.match(schema.properties.docId.description || '', /d_.*24.*hex/);
  assert.equal(create['x-octo-response-unwrap'], undefined);
  assert.equal(create['x-octo-strict-response-schema'], undefined);
  assert.equal(schema.required, undefined, 'do not require PPT-only fields on other doc types');
  assert.equal(schema.properties.templateId.enum, undefined, 'historical receipts may carry retired IDs');
  assert.match(schema.example?.docId || '', /^d_[a-f0-9]{24}$/);
  assert.equal(schema.example.docType, 'html_ppt');
  assert.equal(schema.example.templateId, 'blank');
  for (const [field, value] of Object.entries(schema.example)) {
    const type = schema.properties[field]?.type;
    assert.ok(type, `example field ${field} must be documented`);
    assert.equal(type === 'integer' ? Number.isInteger(value) : typeof value === type, true, field);
  }
});

test('CI re-includes tested Markdown and runs the replacement offline checks', () => {
  const source = read('.github/workflows/ci.yml');
  const push = source.split('  push:\n')[1]?.split('  pull_request:\n')[0] || '';
  assert.match(push, /\n    paths:\n/);
  for (const path of ['README.md', 'CHANGELOG.md']) {
    assert.ok(push.indexOf(`- '${path}'`) > push.indexOf("- '!*.md'"));
  }
  assert.ok(push.indexOf("- 'docs/ppt-release-gate.md'") > push.indexOf("- '!docs/**'"));
  assert.match(source, /tested_docs:\n\s+- '\{README\.md,CHANGELOG\.md,docs\/ppt-release-gate\.md\}'/);
  const output = source.split('\n').find(line => line.trimStart().startsWith('code: ${{')) || '';
  assert.ok(output.includes("|| steps.filter.outputs.tested_docs == 'true'"));
  assert.match(source, /predicate-quantifier: 'every'/);
  assert.match(source, /run: node --test scripts\/release-workflows\.test\.mjs/);
});

test('all workflows are independent of the removed PPT environment and probe', () => {
  const workflows = new URL('../.github/workflows/', import.meta.url);
  for (const file of readdirSync(workflows).filter(file => /\.ya?ml$/.test(file))) {
    const source = readFileSync(new URL(file, workflows), 'utf8');
    assert.doesNotMatch(source, /PPT_RELEASE_|ppt-gallery-(?:release|dry-run)|ppt-preflight|ppt-release-preflight/, file);
    // A removed local reusable workflow must not leave a dangling caller.
    for (const match of source.matchAll(/uses:\s*\.\/(\.github\/workflows\/[^\s]+\.ya?ml)/g)) {
      assert.ok(existsSync(new URL(`../${match[1]}`, import.meta.url)), `${file}: missing ${match[1]}`);
    }
  }
  for (const path of ['.github/workflows/ppt-release-preflight.yml', 'scripts/ppt-release-preflight.mjs']) {
    assert.equal(existsSync(new URL(`../${path}`, import.meta.url)), false, path);
  }
});

test('GitHub Release retains its CI gate and single-build publication ordering', () => {
  const source = read('.github/workflows/release-publish.yml');
  assert.match(source, /publish:\n\s+uses: Mininglamp-OSS\/\.github\/\.github\/workflows\/reusable-release-publish.yml@v1/);
  assert.match(source, /validate_run_id: \$\{\{ inputs.validate_run_id \}\}/);
  assert.match(source, /build:\n\s+needs: publish\n\s+if: \$\{\{ inputs.draft == false \}\}/);
  assert.match(source, /release-upload:\n\s+needs: \[publish, build\]/);
  assert.match(source, /publish-npm:\n\s+needs: \[publish, build, release-upload\]/);
  assert.match(source, /from_artifact: true/);
  assert.match(source, /NPM_TOKEN: \$\{\{ secrets.NPM_TOKEN \}\}/);
  assert.match(source, /cleanup-dist-artifact:\n\s+needs: \[build, release-upload, publish-npm\]/);
});

test('npm retains its release, CI, checksum, packaging and dry-run protections', () => {
  const source = read('.github/workflows/npm-publish.yml');
  assert.match(source, /workflow_call:/);
  assert.match(source, /workflow_dispatch:/);
  assert.match(source, /publish:\n\s+runs-on: ubuntu-latest/);
  for (const name of ['Verify release exists and is published', 'Verify CI evidence for the tagged commit']) {
    assert.ok(source.includes(`- name: ${name}\n        if: steps.version.outputs.DRY_RUN == ''`));
  }
  assert.match(source, /node --test scripts\/prepare-packages.test.js/);
  assert.match(source, /sha256sum --check "\$subset"/);
  assert.match(source, /inputs.from_artifact == true/);
  assert.match(source, /inputs.from_artifact != true/);
  assert.match(source, /NODE_AUTH_TOKEN: \$\{\{ steps.version.outputs.DRY_RUN == '' && secrets.NPM_TOKEN \|\| '' \}\}/);
  assert.match(source, /npm publish .* --dry-run/);
  assert.match(source, /cancel-in-progress: false/);
});

test('published guidance does not require removed release configuration', () => {
  for (const file of ['CHANGELOG.md', 'skills/octo-docs/ppt.md', 'docs/ppt-release-gate.md']) {
    assert.doesNotMatch(read(file), /PPT_RELEASE_|ppt-gallery-(?:release|dry-run)|ppt-release-preflight/);
  }
});
