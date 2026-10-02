import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { openingFromActions, prepareActionsOpening, validateSlackReceipt, createGitHubClient } from './actions-intake.mjs';

const REPO = { id: 1295779810, full_name: 'dsx-ai-factory/health-validation' };
const NODE_RUNTIME_KEYS = ['JS_BINARY__NODE_BINARY', 'JS_BINARY__NODE_PATCHES', 'JS_BINARY__NODE_WRAPPER',
  'JS_BINARY__PATCH_NODE_FS', 'JS_BINARY__FS_PATCH_ROOTS'];
const nodeRuntimeEnvironment = () => Object.fromEntries(NODE_RUNTIME_KEYS
  .filter(key => typeof process.env[key] === 'string').map(key => [key, process.env[key]]));

const ROUTING = { channel_id: 'C0123456789', mention_user_ids: ['U0123456789', 'U9876543210'] };
const NOW = '2026-10-02T20:00:00Z';
const raw = (patch = {}) => ({ node_id: 'PR_example', number: 17, state: 'open', draft: true,
  created_at: NOW, updated_at: NOW, user: { id: 1001, login: 'outside-user', type: 'User' },
  labels: [{ name: 'bug' }], assignees: [{ login: 'maintainer', id: 10 }], base: { repo: REPO }, ...patch });
const event = (patch = {}) => ({ action: 'opened', repository: REPO, pull_request: raw(), ...patch });
const review = (patch = {}) => ({ author_id: 1001, login: 'outside-user', affiliation: 'external',
  reviewed_by: 'synthetic-reviewer', evidence_ref: 'synthetic-private-evidence', revision: 'test-1',
  reviewed_at: '2026-10-02T19:00:00Z', expires_at: '2026-10-03T19:00:00Z', ...patch });
function api(current = raw()) {
  const writes = [];
  return { writes, readItem: async () => structuredClone(current),
    addLabels: async (number, labels) => { writes.push({ op: 'add', number, labels }); },
    removeLabel: async (number, label) => { writes.push({ op: 'remove', number, label }); } };
}
const args = (patch = {}) => ({ eventName: 'pull_request_target', payload: event(),
  evaluatedAt: NOW, routing: ROUTING, reviewedDecisions: [], ...patch });

test('Actions draft opening uses base scope and original numeric author identity', () => {
  const item = openingFromActions('pull_request_target', event(), ROUTING);
  assert.equal(item.draft, true);
  assert.equal(item.user.id, 1001);
  assert.equal(item.kind, 'pr');
  assert.equal(item.repository.id, 1295779810);
});

test('issue opening is supported while comments and PR updates are ignored', () => {
  assert.equal(openingFromActions('issues', event({ issue: raw({ node_id: 'I_example' }) }), ROUTING).kind, 'issue');
  assert.equal(openingFromActions('issue_comment', event(), ROUTING), null);
  assert.equal(openingFromActions('pull_request_target', event({ action: 'synchronize' }), ROUTING), null);
  assert.throws(() => openingFromActions('issues', event({ repository: { ...REPO, id: 1 }, issue: raw() }), ROUTING), /scope/);
});

test('unverified draft gets safe payload and intent after affiliation label', async () => {
  const github = api();
  const result = await prepareActionsOpening(args(), github);
  assert.equal(result.status, 'send');
  assert.equal(result.decision.affiliation, 'unverified');
  assert.deepEqual(github.writes, [
    { op: 'add', number: 17, labels: ['affiliation/unverified'] },
    { op: 'add', number: 17, labels: ['triage/alert-requested'] },
  ]);
  assert.equal(result.payload.channel, 'C0123456789');
  assert.match(result.payload.text, /<@U0123456789> <@U9876543210>/);
  assert.match(result.payload.text, /draft/);
  assert.equal(result.delivered, false);
});

test('registry review preserves unrelated labels and assignees while correcting affiliation', async () => {
  const current = raw({ labels: [{ name: 'affiliation/unverified' }, { name: 'bug' }] });
  const github = api(current);
  const result = await prepareActionsOpening(args({ reviewedDecisions: [review()] }), github);
  assert.equal(result.decision.affiliation, 'external');
  assert.deepEqual(github.writes, [
    { op: 'add', number: 17, labels: ['affiliation/external'] },
    { op: 'remove', number: 17, label: 'affiliation/unverified' },
    { op: 'add', number: 17, labels: ['triage/alert-requested'] },
  ]);
  assert.ok(result.decision.labels_after.includes('bug'));
  assert.deepEqual(result.decision.assignees, [{ login: 'maintainer', id: 10 }]);
  assert.equal(JSON.stringify(result.payload).includes('synthetic-private-evidence'), false);
});

test('existing request intent requires reconciliation even after affiliation correction', async () => {
  const github = api(raw({ labels: [{ name: 'triage/alert-requested' }, { name: 'affiliation/unverified' }] }));
  const result = await prepareActionsOpening(args({ reviewedDecisions: [review()] }), github);
  assert.equal(result.status, 'reconcile');
  assert.equal(result.payload, null);
  assert.equal(github.writes.some(write => write.labels?.includes('triage/alert-requested')), false);
});

test('valid NVIDIA review and actual Bot suppress posts', async () => {
  for (const [current, reviews, affiliation] of [
    [raw(), [review({ affiliation: 'nvidia' })], 'nvidia'],
    [raw({ user: { id: 9001, login: 'dependabot[bot]', type: 'Bot' } }), [], 'bot'],
  ]) {
    const github = api(current);
    const result = await prepareActionsOpening(args({ reviewedDecisions: reviews }), github);
    assert.equal(result.status, 'suppressed');
    assert.equal(result.decision.affiliation, affiliation);
    assert.equal(result.payload, null);
    assert.equal(github.writes.some(write => write.labels?.includes('triage/alert-requested')), false);
  }
});

test('mismatched authoritative item and failed label reservation cannot produce send payload', async () => {
  await assert.rejects(prepareActionsOpening(args(), api(raw({ node_id: 'OTHER' }))), /identity/);
  const github = api(); github.addLabels = async () => { throw new Error('synthetic write failure'); };
  await assert.rejects(prepareActionsOpening(args(), github), /write failure/);
});

test('receipt validation requires actual success, intended channel and Slack timestamp', () => {
  const good = { ok: 'true', channel_id: 'C0123456789', ts: '1790961600.123456' };
  assert.deepEqual(validateSlackReceipt(good, ROUTING), { channel_id: 'C0123456789', ts: '1790961600.123456' });
  for (const patch of [{ ok: 'false' }, { ok: undefined }, { channel_id: 'OTHER' }, { ts: '' }, { ts: 'invalid' }]) {
    assert.throws(() => validateSlackReceipt({ ...good, ...patch }, ROUTING), /receipt/);
  }
});

test('GitHub adapter fixes scope, uses JSON bodies and preserves unrelated labels through additive requests', async () => {
  const calls = [];
  const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async (url, options) => {
    calls.push({ url, method: options.method ?? 'GET', body: options.body });
    return { ok: true, status: 200, json: async () => options.method === 'POST'
      ? JSON.parse(options.body).labels.map(name => ({ name }))
      : options.method === 'DELETE' ? [{ name: 'bug' }, { name: 'affiliation/unverified' }] : raw() };
  } });
  await github.readItem('pr', 17);
  await github.addLabels(17, ['affiliation/unverified']);
  await github.removeLabel(17, 'affiliation/nvidia');
  assert.deepEqual(calls, [
    { url: 'https://api.github.com/repos/dsx-ai-factory/health-validation/pulls/17', method: 'GET', body: undefined },
    { url: 'https://api.github.com/repos/dsx-ai-factory/health-validation/issues/17/labels', method: 'POST', body: '{"labels":["affiliation/unverified"]}' },
    { url: 'https://api.github.com/repos/dsx-ai-factory/health-validation/issues/17/labels/affiliation%2Fnvidia', method: 'DELETE', body: undefined },
  ]);
  await assert.rejects(github.readItem('pr', '17/../../secrets'));
  await assert.rejects(github.removeLabel(17, 'bug'));
});

test('failed GitHub responses are reported without response-body or credential leakage', async () => {
  const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async () => ({
    ok: false, status: 403, json: async () => ({ message: 'PRIVATE_RESPONSE' }),
  }) });
  await assert.rejects(github.readItem('pr', 17), error => /403/.test(error.message) && !/PRIVATE|token/.test(error.message));
});

test('receipt CLI validates success without printing private receipt values', () => {
  const script = fileURLToPath(new URL('./actions-intake.mjs', import.meta.url));
  const run = spawnSync(process.execPath, [script, 'validate-receipt'], { encoding: 'utf8',
    env: { ...nodeRuntimeEnvironment(), HEALTH_VALIDATION_INTAKE_CHANNEL: 'C0123456789', HEALTH_VALIDATION_INTAKE_MENTION_USER_IDS_JSON: '["U0123456789"]', HEALTH_INTAKE_SLACK_OK: 'true', HEALTH_INTAKE_SLACK_CHANNEL: 'C0123456789',
      HEALTH_INTAKE_SLACK_TS: '1790961600.123456' } });
  assert.equal(run.status, 0, run.stderr);
  assert.match(run.stdout, /Receipt validated/);
  assert.equal(run.stdout.includes('1790961600.123456'), false);
  assert.equal(run.stdout.includes('C0123456789'), false);
  const invalid = spawnSync(process.execPath, [script, 'validate-receipt'], { encoding: 'utf8',
    env: { ...nodeRuntimeEnvironment(), HEALTH_VALIDATION_INTAKE_CHANNEL: 'C0123456789', HEALTH_VALIDATION_INTAKE_MENTION_USER_IDS_JSON: '["U0123456789"]', HEALTH_INTAKE_SLACK_OK: 'true', HEALTH_INTAKE_SLACK_CHANNEL: 'OTHER',
      HEALTH_INTAKE_SLACK_TS: 'PRIVATE_RECEIPT' } });
  assert.equal(invalid.status, 1);
  assert.equal(invalid.stderr.includes('PRIVATE_RECEIPT'), false);
});

test('prepare CLI is disabled by default before any metadata access or label write', () => {
  const script = fileURLToPath(new URL('./actions-intake.mjs', import.meta.url));
  const run = spawnSync(process.execPath, [script, 'prepare'], { encoding: 'utf8',
    env: { ...nodeRuntimeEnvironment(), GITHUB_TOKEN: 'PRIVATE_TEST_CREDENTIAL', GITHUB_EVENT_PATH: '/missing/event.json' } });
  assert.equal(run.status, 1);
  assert.equal(run.stderr.includes('PRIVATE_TEST_CREDENTIAL'), false);
  assert.equal(run.stdout, '');
});

test('invalid routing fails before GitHub reads or label writes', async () => {
  const github = api(); let reads = 0;
  github.readItem = async () => { reads++; return raw(); };
  await assert.rejects(prepareActionsOpening(args({ routing: { ...ROUTING, mention_user_ids: ['<!here>'] } }), github), /routing/i);
  assert.equal(reads, 0);
  assert.deepEqual(github.writes, []);
});

test('receipt must match the configured destination rather than an implicit channel', () => {
  const receipt = { ok: true, channel_id: 'C9876543210', ts: '1790961600.123456' };
  const route = { ...ROUTING, channel_id: 'C9876543210' };
  assert.deepEqual(validateSlackReceipt(receipt, route), { channel_id: 'C9876543210', ts: '1790961600.123456' });
  assert.throws(() => validateSlackReceipt(receipt, ROUTING), /receipt/i);
  assert.throws(() => validateSlackReceipt(receipt), /routing/i);
});

test('configuration CLI validates opaque routing without printing route values', () => {
  const script = fileURLToPath(new URL('./actions-intake.mjs', import.meta.url));
  const good = spawnSync(process.execPath, [script, 'validate-config'], { encoding: 'utf8',
    env: { ...nodeRuntimeEnvironment(), HEALTH_VALIDATION_INTAKE_CHANNEL: 'C0123456789', HEALTH_VALIDATION_INTAKE_MENTION_USER_IDS_JSON: '["U0123456789"]' } });
  assert.equal(good.status, 0, good.stderr);
  assert.equal((good.stdout + good.stderr).includes('C0123456789'), false);
  assert.equal((good.stdout + good.stderr).includes('U0123456789'), false);
  for (const mentions of ['', '{"PRIVATE_VALUE":true}', '["<!everyone>"]']) {
    const bad = spawnSync(process.execPath, [script, 'validate-config'], { encoding: 'utf8',
      env: { ...nodeRuntimeEnvironment(), HEALTH_VALIDATION_INTAKE_CHANNEL: 'C0123456789', HEALTH_VALIDATION_INTAKE_MENTION_USER_IDS_JSON: mentions } });
    assert.equal(bad.status, 1);
    assert.equal((bad.stdout + bad.stderr).includes('PRIVATE_VALUE'), false);
  }
});

function runPrepareCli({ registry = '[]', routing = ROUTING, output = true } = {}) {
  const directory = mkdtempSync(join(tmpdir(), 'intake-actions-cli-'));
  try {
    const eventPath = join(directory, 'event.json');
    const outputPath = join(directory, 'output');
    const callsPath = join(directory, 'calls');
    writeFileSync(eventPath, JSON.stringify(event()));
    writeFileSync(outputPath, ''); writeFileSync(callsPath, '');
    const preload = 'data:text/javascript,' + encodeURIComponent(String.raw`
      import {appendFileSync} from 'node:fs';
      globalThis.fetch = async (url, options) => {
        appendFileSync(process.env.TEST_API_CALLS, options.method + '\n');
        return {ok: true, status: 200, json: async () => options.method === 'POST'
          ? JSON.parse(options.body).labels.map(name => ({name})) : JSON.parse(process.env.TEST_CURRENT_ITEM)};
      };
    `);
    const env = { ...nodeRuntimeEnvironment(), HEALTH_VALIDATION_APPLY_LABELS: 'true', GITHUB_TOKEN: 'synthetic-token',
      GITHUB_EVENT_NAME: 'pull_request_target', GITHUB_EVENT_PATH: eventPath,
      HEALTH_VALIDATION_INTAKE_CHANNEL: routing.channel_id,
      HEALTH_VALIDATION_INTAKE_MENTION_USER_IDS_JSON: JSON.stringify(routing.mention_user_ids),
      TEST_API_CALLS: callsPath, TEST_CURRENT_ITEM: JSON.stringify(raw()) };
    if (registry !== null) env.HEALTH_VALIDATION_REVIEWED_DECISIONS_JSON = registry;
    if (output) env.GITHUB_OUTPUT = outputPath;
    const script = fileURLToPath(new URL('./actions-intake.mjs', import.meta.url));
    const run = spawnSync(process.execPath, ['--import', preload, script, 'prepare'], {
      env, encoding: 'utf8', timeout: 5000,
    });
    return { ...run, output: readFileSync(outputPath, 'utf8'), calls: readFileSync(callsPath, 'utf8') };
  } finally { rmSync(directory, { recursive: true, force: true }); }
}

test('prepare CLI rejects routing before any authoritative read or label write', () => {
  const run = runPrepareCli({ routing: { ...ROUTING, mention_user_ids: ['<!everyone>'] } });
  assert.equal(run.status, 1);
  assert.equal(run.calls, '');
  assert.equal(run.output, '');
});

test('prepare CLI requires Actions output before reading or writing GitHub', () => {
  const run = runPrepareCli({ output: false });
  assert.equal(run.status, 1);
  assert.equal(run.calls, '');
});

for (const [name, registry] of [
  ['missing', null], ['invalid JSON', 'PRIVATE_REGISTRY_CONTENT'],
  ['wrong collection', '{"PRIVATE_REGISTRY_FIELD":true}'],
  ['malformed relevant record', JSON.stringify([review({ expires_at: null })])],
]) {
  test(`${name} registry warns generically while preserving unknown alerts`, () => {
    const run = runPrepareCli({ registry });
    assert.equal(run.status, 0, run.stderr);
    assert.match(run.stderr, /evidence/i);
    assert.match(run.output, /status=send/);
    assert.match(run.output, /affiliation unverified/);
    assert.equal((run.stdout + run.stderr + run.output).includes('PRIVATE_REGISTRY'), false);
    assert.equal((run.stdout + run.stderr + run.output).includes('synthetic-private-evidence'), false);
    assert.equal((run.stdout + run.stderr + run.output).includes('synthetic-reviewer'), false);
  });
}

for (const [name, response] of [
  ['missing requested label', [{ name: 'bug' }]], ['empty label array', []],
  ['malformed response', { message: 'PRIVATE_RESPONSE' }], ['malformed label', [{ name: null }]],
]) {
  test(`successful HTTP status with ${name} cannot reserve an alert`, async () => {
    const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async () => ({
      ok: true, status: 200, json: async () => response,
    }) });
    await assert.rejects(github.addLabels(17, ['triage/alert-requested']), error =>
      /unverified|reconcile/i.test(error.message) && !error.message.includes('PRIVATE_RESPONSE'));
  });
}

test('missing intent in HTTP-success response prevents the opening send plan', async () => {
  const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async (url, options) => ({
    ok: true, status: 200, json: async () => {
      if (options.method === 'GET') return raw();
      const labels = JSON.parse(options.body).labels;
      return labels.includes('triage/alert-requested') ? [] : labels.map(name => ({ name }));
    },
  }) });
  await assert.rejects(prepareActionsOpening(args(), github), /unverified|reconcile/i);
});

for (const [name, response] of [
  ['retained affiliation', [{ name: 'affiliation/nvidia' }]],
  ['malformed response', { message: 'PRIVATE_REMOVAL_RESPONSE' }],
  ['null response', null], ['malformed label', [{ name: null }]],
]) {
  test(`label removal rejects HTTP-success with ${name} before alert intent`, async () => {
    const calls = [];
    const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async (url, options) => {
      calls.push({ method: options.method, body: options.body });
      return { ok: true, status: 200, json: async () => {
        if (options.method === 'GET') return raw({ labels: [{ name: 'affiliation/nvidia' }] });
        if (options.method === 'DELETE') return response;
        return JSON.parse(options.body).labels.map(name => ({ name }));
      } };
    } });
    await assert.rejects(prepareActionsOpening(args(), github), error =>
      /unverified|reconcile/i.test(error.message) && !error.message.includes('PRIVATE_REMOVAL_RESPONSE'));
    assert.deepEqual(calls.map(call => call.method), ['GET', 'POST', 'DELETE']);
    assert.equal(calls.some(call => call.body?.includes('triage/alert-requested')), false);
  });
}

test('unreadable label removal response stops before alert intent without leaking response data', async () => {
  const calls = [];
  const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async (url, options) => {
    calls.push(options.method);
    return { ok: true, status: 200, json: async () => {
      if (options.method === 'GET') return raw({ labels: [{ name: 'affiliation/nvidia' }] });
      if (options.method === 'DELETE') throw new Error('PRIVATE_REMOVAL_BODY');
      return JSON.parse(options.body).labels.map(name => ({ name }));
    } };
  } });
  await assert.rejects(prepareActionsOpening(args(), github), error =>
    /invalid|reconcile/i.test(error.message) && !error.message.includes('PRIVATE_REMOVAL_BODY'));
  assert.deepEqual(calls, ['GET', 'POST', 'DELETE']);
});

test('verified remaining labels allow the opening send plan after affiliation removal', async () => {
  const calls = [];
  const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async (url, options) => {
    calls.push(options.method);
    return { ok: true, status: 200, json: async () => {
      if (options.method === 'GET') return raw({ labels: [{ name: 'bug' }, { name: 'affiliation/nvidia' }] });
      if (options.method === 'DELETE') return [{ name: 'bug' }, { name: 'affiliation/unverified' }];
      return JSON.parse(options.body).labels.map(name => ({ name }));
    } };
  } });
  const result = await prepareActionsOpening(args(), github);
  assert.equal(result.status, 'send');
  assert.deepEqual(calls, ['GET', 'POST', 'DELETE', 'POST']);
});

test('only DELETE 404 means an affiliation label is already absent', async () => {
  let jsonReads = 0;
  const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async () => ({
    ok: false, status: 404, json: async () => { jsonReads++; throw new Error('PRIVATE_404_BODY'); },
  }) });
  await github.removeLabel(17, 'affiliation/nvidia');
  await assert.rejects(github.readItem('pr', 17), /404/);
  await assert.rejects(github.addLabels(17, ['affiliation/unverified']), /404/);
  assert.equal(jsonReads, 0);
});
