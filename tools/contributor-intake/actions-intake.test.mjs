import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { EventEmitter } from 'node:events';
import { Duplex } from 'node:stream';
import https from 'node:https';
import { openingFromActions, prepareActionsOpening, createGitHubClient, createSlackClient } from './actions-intake.mjs';

const REPO = { id: 1295779810, full_name: 'dsx-ai-factory/health-validation' };
const NODE_RUNTIME_KEYS = ['JS_BINARY__NODE_BINARY', 'JS_BINARY__NODE_PATCHES', 'JS_BINARY__NODE_WRAPPER',
  'JS_BINARY__PATCH_NODE_FS', 'JS_BINARY__FS_PATCH_ROOTS'];
const nodeRuntimeEnvironment = () => Object.fromEntries(NODE_RUNTIME_KEYS
  .filter(key => typeof process.env[key] === 'string').map(key => [key, process.env[key]]));

const ROUTING = { channel_id: 'C0123456789', mention_user_ids: ['U0123456789', 'U9876543210'] };
const NOW = '2026-10-02T20:00:00Z';
const raw = (patch = {}) => ({ node_id: 'PR_example', number: 17, state: 'open', draft: true,
  created_at: NOW, user: { id: 1001, login: 'outside-user', type: 'User' },
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
  assert.deepEqual(Object.keys(result).sort(), ['decision', 'payload', 'status']);
  assert.equal(result.payload.text, result.decision.message_text);
});

test('registry review preserves unrelated labels and assignees while correcting affiliation', async () => {
  const current = raw({ labels: [{ name: 'affiliation/unverified' }, { name: 'bug' }] });
  const before = structuredClone(current);
  const github = api(current);
  const result = await prepareActionsOpening(args({ reviewedDecisions: [review()] }), github);
  assert.equal(result.decision.affiliation, 'external');
  assert.deepEqual(github.writes, [
    { op: 'add', number: 17, labels: ['affiliation/external'] },
    { op: 'remove', number: 17, label: 'affiliation/unverified' },
    { op: 'add', number: 17, labels: ['triage/alert-requested'] },
  ]);
  assert.deepEqual(current, before);
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

test('invalid routing fails before GitHub reads or label writes', async () => {
  const github = api(); let reads = 0;
  github.readItem = async () => { reads++; return raw(); };
  await assert.rejects(prepareActionsOpening(args({ routing: { ...ROUTING, mention_user_ids: ['<!here>'] } }), github), /routing/i);
  assert.equal(reads, 0);
  assert.deepEqual(github.writes, []);
});

test('Slack sender validates the configured destination without an implicit channel', async () => {
  const receipt = { ok: true, channel: 'C9876543210', ts: '1790961600.123456' };
  const route = { ...ROUTING, channel_id: receipt.channel };
  const client = createSlackClient({ token: 'synthetic-slack-token', requestFn: coreRequestReply({ receipt }) });
  assert.deepEqual(await client.sendNotification({ ...notificationPayload, channel: receipt.channel }, route), {
    channel_id: receipt.channel, ts: receipt.ts,
  });
  await assert.rejects(client.sendNotification(notificationPayload, ROUTING), /Slack.*reconcile/i);
  await assert.rejects(client.sendNotification(notificationPayload), /routing/i);
});

function runNotifyCli({ registry = '[]', routingJson = JSON.stringify(ROUTING), current = raw(), mode = 'notify',
    githubToken = 'synthetic-github-token', slackToken = 'synthetic-slack-token', apply = 'true',
    receipt = { ok: true, channel: ROUTING.channel_id, ts: '1790961600.123456' }, slackStatus = 200,
    unreadableSlackBody = false, outputAvailable = true } = {}) {
  const directory = mkdtempSync(join(tmpdir(), 'intake-actions-cli-'));
  try {
    const eventPath = join(directory, 'event.json');
    const outputPath = join(directory, 'output');
    const callsPath = join(directory, 'calls');
    writeFileSync(eventPath, JSON.stringify(event()));
    writeFileSync(outputPath, ''); writeFileSync(callsPath, '');
    const preload = 'data:text/javascript,' + encodeURIComponent(String.raw`
      import {appendFileSync} from 'node:fs';
      import https from 'node:https';
      import {EventEmitter} from 'node:events';
      import {syncBuiltinESMExports} from 'node:module';
      https.request = (url, options, callback) => {
        const request = new EventEmitter();
        request.destroy = () => {};
        request.end = body => {
          appendFileSync(process.env.TEST_API_CALLS, JSON.stringify({url, method: options.method,
            payload: JSON.parse(body)}) + '\n');
          queueMicrotask(() => {
            const response = new EventEmitter();
            response.statusCode = Number(process.env.TEST_SLACK_STATUS);
            response.destroy = () => {};
            callback(response);
            const body = process.env.TEST_UNREADABLE_SLACK_BODY === 'true' ? 'PRIVATE_SLACK_BODY' : process.env.TEST_SLACK_RECEIPT;
            response.emit('data', Buffer.from(body));
            response.emit('end');
            response.emit('close');
          });
        };
        return request;
      };
      syncBuiltinESMExports();
      globalThis.fetch = async (url, options) => {
        appendFileSync(process.env.TEST_API_CALLS, JSON.stringify({url, method: options.method,
          payload: options.body ? JSON.parse(options.body) : null}) + '\n');
        if (!url.startsWith('https://api.github.com/repos/dsx-ai-factory/health-validation/')) throw new Error('unexpected fetch URL');
        return {ok: true, status: 200, json: async () => options.method === 'POST'
          ? JSON.parse(options.body).labels.map(name => ({name})) : options.method === 'DELETE'
          ? [] : JSON.parse(process.env.TEST_CURRENT_ITEM)};
      };
    `);
    const env = { ...nodeRuntimeEnvironment(), HEALTH_VALIDATION_APPLY_LABELS: apply, GITHUB_TOKEN: githubToken,
      HEALTH_VALIDATION_SLACK_BOT_TOKEN: slackToken,
      GITHUB_EVENT_NAME: 'pull_request_target', GITHUB_EVENT_PATH: eventPath,
      TEST_API_CALLS: callsPath, TEST_CURRENT_ITEM: JSON.stringify(current),
      TEST_SLACK_RECEIPT: JSON.stringify(receipt), TEST_SLACK_STATUS: String(slackStatus),
      TEST_UNREADABLE_SLACK_BODY: String(unreadableSlackBody) };
    if (routingJson !== null) env.HEALTH_VALIDATION_INTAKE_ROUTING_JSON = routingJson;
    if (outputAvailable) env.GITHUB_OUTPUT = outputPath;
    if (registry !== null) env.HEALTH_VALIDATION_REVIEWED_DECISIONS_JSON = registry;
    const script = fileURLToPath(new URL('./actions-intake.mjs', import.meta.url));
    const run = spawnSync(process.execPath, ['--import', preload, script, mode], {
      env, encoding: 'utf8', timeout: 5000,
    });
    return { ...run, output: readFileSync(outputPath, 'utf8'),
      calls: readFileSync(callsPath, 'utf8').trim().split('\n').filter(Boolean).map(line => JSON.parse(line)) };
  } finally { rmSync(directory, { recursive: true, force: true }); }
}

for (const [name, patch] of [
  ['missing routing', { routingJson: null }], ['malformed routing JSON', { routingJson: 'PRIVATE_ROUTING_JSON' }],
  ['wrong routing collection', { routingJson: '[]' }],
  ['routing injection', { routingJson: JSON.stringify({ ...ROUTING, mention_user_ids: ['<!everyone>'] }) }],
  ['missing GitHub credential', { githubToken: '' }], ['missing Slack credential', { slackToken: '' }],
  ['disabled application', { apply: '' }],
]) {
  test(`notification CLI rejects ${name} before GitHub or Slack access`, () => {
    const run = runNotifyCli(patch);
    assert.equal(run.status, 1);
    assert.deepEqual(run.calls, []);
    assert.equal(run.output, '');
    assert.equal(run.stdout, '');
    assert.equal(run.stderr.includes('PRIVATE_ROUTING_JSON'), false);
  });
}

for (const [name, registry] of [
  ['missing', null], ['invalid JSON', 'PRIVATE_REGISTRY_CONTENT'],
  ['wrong collection', '{"PRIVATE_REGISTRY_FIELD":true}'],
  ['malformed relevant record', JSON.stringify([review({ expires_at: null })])],
]) {
  test(`${name} registry warns generically while preserving unknown alerts`, () => {
    const run = runNotifyCli({ registry });
    assert.equal(run.status, 0, run.stderr);
    assert.match(run.stderr, /evidence/i);
    assert.match(run.calls.at(-1).payload.text, /affiliation unverified/);
    assert.equal(run.output, '');
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

test('a sparse requested label array is refused before a GitHub request', async () => {
  let calls = 0;
  const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async () => {
    calls++;
    return { ok: true, status: 200, json: async () => [{ name: 'affiliation/unverified' }] };
  } });
  await assert.rejects(github.addLabels(17, Array(1)), /scope|invalid/i);
  assert.equal(calls, 0);
});

test('a sparse acknowledged label array cannot prove an affiliation was removed', async () => {
  const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async () => ({
    ok: true, status: 200, json: async () => Array(1),
  }) });
  await assert.rejects(github.removeLabel(17, 'affiliation/nvidia'), /unverified|reconcile/i);
});

const notificationPayload = { channel: ROUTING.channel_id, text: 'synthetic notification',
  unfurl_links: false, unfurl_media: false };

function coreRequestReply(options = {}, calls = []) {
  return (url, requestOptions, callback) => {
    const request = new EventEmitter();
    request.destroyed = false;
    request.destroy = () => { request.destroyed = true; };
    request.end = body => {
      calls.push({ url, options: requestOptions, body, request });
      queueMicrotask(() => {
        if (options.requestError) { request.emit('error', new Error('PRIVATE_SLACK_REQUEST')); return; }
        const response = new EventEmitter();
        response.statusCode = options.status ?? 200;
        response.destroyed = false;
        response.destroy = () => { response.destroyed = true; };
        callback(response);
        if (request.destroyed || response.destroyed) return;
        if (options.responseError) { response.emit('error', new Error('PRIVATE_SLACK_RESPONSE')); return; }
        if (options.abortBody) { response.emit('aborted'); return; }
        if (options.closeBody) { response.emit('close'); return; }
        const text = options.body ?? JSON.stringify(options.receipt ?? {
          ok: true, channel: ROUTING.channel_id, ts: '1790961600.123456',
        });
        for (const chunk of options.chunks ?? [Buffer.from(text)]) {
          response.emit('data', chunk);
          if (request.destroyed || response.destroyed) return;
        }
        response.emit('end');
        response.emit('close');
      });
    };
    return request;
  };
}

test('Slack sender makes one core HTTPS request and validates actual JSON receipt', async () => {
  const calls = [];
  const client = createSlackClient({ token: 'synthetic-slack-token', requestFn: coreRequestReply({}, calls) });
  assert.deepEqual(await client.sendNotification(notificationPayload, ROUTING), {
    channel_id: ROUTING.channel_id, ts: '1790961600.123456',
  });
  assert.equal(calls.length, 1);
  const [{ url, options, body }] = calls;
  assert.equal(url, 'https://slack.com/api/chat.postMessage');
  assert.equal(options.method, 'POST');
  assert.equal(options.agent, false);
  assert.equal(options.headers.Authorization, 'Bearer synthetic-slack-token');
  assert.equal(options.headers['Content-Type'], 'application/json');
  assert.equal(options.headers['Content-Length'], Buffer.byteLength(body));
  assert.deepEqual(JSON.parse(body), notificationPayload);
});

for (const [name, options] of [
  ['HTTP 421', { status: 421 }], ['HTTP 429', { status: 429 }], ['HTTP rejection', { status: 403 }],
  ['redirect', { status: 302 }], ['connection error', { requestError: true }],
  ['unparseable response', { body: 'PRIVATE_SLACK_JSON' }], ['body error', { responseError: true }],
  ['aborted body', { abortBody: true }], ['premature body close', { closeBody: true }],
  ['oversized body', { chunks: [Buffer.alloc(32768), Buffer.alloc(32769)] }],
  ['ok false', { receipt: { ok: false, error: 'PRIVATE_SLACK_ERROR' } }],
  ['string success', { receipt: { ok: 'true', channel: ROUTING.channel_id, ts: '1790961600.123456' } }],
  ['missing success', { receipt: { channel: ROUTING.channel_id, ts: '1790961600.123456' } }],
  ['empty timestamp', { receipt: { ok: true, channel: ROUTING.channel_id, ts: '' } }],
  ['wrong channel', { receipt: { ok: true, channel: 'C9876543210', ts: '1790961600.123456' } }],
  ['invalid timestamp', { receipt: { ok: true, channel: ROUTING.channel_id, ts: 'PRIVATE_SLACK_TIMESTAMP' } }],
]) {
  test(`Slack ${name} has one request and no private error leakage`, async () => {
    const calls = [];
    const client = createSlackClient({ token: 'synthetic-slack-token', requestFn: coreRequestReply(options, calls) });
    await assert.rejects(client.sendNotification(notificationPayload, ROUTING), error =>
      /Slack|receipt/i.test(error.message) && !error.message.includes('PRIVATE_SLACK'));
    assert.equal(calls.length, 1);
  });
}

for (const phase of ['connection', 'receipt body']) {
  test(`Slack absolute ten-second deadline bounds ${phase} without retry`, async t => {
    t.mock.timers.enable({ apis: ['setTimeout'] });
    let calls = 0, respond;
    const request = new EventEmitter();
    request.destroyed = false;
    request.destroy = () => { request.destroyed = true; };
    request.end = () => {};
    const client = createSlackClient({ token: 'synthetic-slack-token', requestFn: (url, options, callback) => {
      calls++; respond = callback; return request;
    } });
    const pending = client.sendNotification(notificationPayload, ROUTING);
    t.mock.timers.tick(6000);
    if (phase === 'receipt body') {
      const response = new EventEmitter();
      response.statusCode = 200;
      response.destroy = () => {};
      respond(response);
      response.emit('data', Buffer.from('{"ok":'));
    }
    t.mock.timers.tick(3999);
    assert.equal(request.destroyed, false);
    t.mock.timers.tick(1);
    await assert.rejects(pending, /Slack.*reconcile/i);
    assert.equal(request.destroyed, true);
    assert.equal(calls, 1);
  });
}

test('native HTTPS HTTP 421 is rejected after exactly one serialized POST', async () => {
  let requests = 0;
  const destinations = [];
  const agent = new https.Agent({ keepAlive: false });
  agent.createConnection = () => {
    let buffered = '', replied = false;
    const socket = new Duplex({
      read() {},
      write(chunk, encoding, done) {
        buffered += chunk.toString();
        const boundary = buffered.indexOf('\r\n\r\n');
        const length = Number(/Content-Length: (\d+)/i.exec(buffered)?.[1] ?? 0);
        if (!replied && boundary >= 0 && Buffer.byteLength(buffered.slice(boundary + 4)) >= length) {
          replied = true;
          assert.match(buffered, /^POST \/api\/chat\.postMessage HTTP\/1\.1\r\n/);
          requests++;
          const body = JSON.stringify({ ok: true, channel: ROUTING.channel_id, ts: '1790961600.123456' });
          const status = requests === 1 ? '421 Misdirected Request' : '200 OK';
          this.push('HTTP/1.1 ' + status + '\r\nContent-Type: application/json\r\nContent-Length: ' +
            Buffer.byteLength(body) + '\r\nConnection: close\r\n\r\n' + body);
          this.push(null);
        }
        done();
      },
    });
    return socket;
  };
  try {
    const client = createSlackClient({ token: 'synthetic-slack-token', requestFn: (url, options, callback) => {
      destinations.push(url);
      return https.request(url, { ...options, agent }, callback);
    } });
    await assert.rejects(client.sendNotification(notificationPayload, ROUTING), /Slack.*reconcile/i);
    assert.deepEqual(destinations, ['https://slack.com/api/chat.postMessage']);
    assert.equal(requests, 1);
  } finally { agent.destroy(); }
});

test('Slack sender refuses missing credential, invalid routing and a different payload channel before access', async () => {
  for (const token of [undefined, '', ' ']) assert.throws(() => createSlackClient({ token }), /credential/i);
  let calls = 0;
  const client = createSlackClient({ token: 'synthetic-slack-token', requestFn: () => { calls++; } });
  await assert.rejects(client.sendNotification(notificationPayload, { ...ROUTING, mention_user_ids: [] }), /routing/i);
  await assert.rejects(client.sendNotification({ ...notificationPayload, channel: 'C9876543210' }, ROUTING), /payload|channel/i);
  assert.equal(calls, 0);
});

test('notification CLI reserves intent before the single post and exports no private payload or receipt', () => {
  const run = runNotifyCli({ outputAvailable: false });
  assert.equal(run.status, 0, run.stderr);
  assert.deepEqual(run.calls.map(call => call.method), ['GET', 'POST', 'POST', 'POST']);
  assert.deepEqual(run.calls[2].payload.labels, ['triage/alert-requested']);
  assert.equal(run.calls[3].url, 'https://slack.com/api/chat.postMessage');
  assert.match(run.calls[3].payload.text, /affiliation unverified; open; draft/);
  assert.match(run.stdout, /Slack confirmed the post/i);
  assert.equal(run.output, '');
  for (const value of [ROUTING.channel_id, ...ROUTING.mention_user_ids, '1790961600.123456', 'outside-user',
    'synthetic-github-token', 'synthetic-slack-token', 'synthetic-private-evidence', 'synthetic-reviewer']) {
    assert.equal((run.stdout + run.stderr).includes(value), false);
  }
  assert.equal(/journal (written|saved)|persisted/i.test(run.stdout), false);
});

test('notification CLI does not write Actions outputs even when an output path is present', () => {
  const run = runNotifyCli();
  assert.equal(run.status, 0, run.stderr);
  assert.equal(run.output, '');
});

test('NVIDIA and Bot CLI openings are suppressed without a Slack post', () => {
  const currentReview = review({ affiliation: 'nvidia',
    reviewed_at: new Date(Date.now() - 60000).toISOString(), expires_at: new Date(Date.now() + 3600000).toISOString() });
  for (const patch of [{ registry: JSON.stringify([currentReview]) },
    { current: raw({ user: { id: 9001, login: 'synthetic-bot[bot]', type: 'Bot' } }) }]) {
    const run = runNotifyCli(patch);
    assert.equal(run.status, 0, run.stderr);
    assert.match(run.stdout, /suppressed/i);
    assert.equal(run.calls.some(call => call.url.startsWith('https://slack.com/')), false);
    assert.equal(run.calls.some(call => call.payload?.labels?.includes('triage/alert-requested')), false);
    assert.equal(run.output, '');
  }
});

test('existing intent requires CLI reconciliation and never resends', () => {
  const run = runNotifyCli({ current: raw({ labels: [{ name: 'affiliation/unverified' }, { name: 'triage/alert-requested' }] }) });
  assert.equal(run.status, 2, run.stderr);
  assert.match(run.stdout, /reconcile/i);
  assert.equal(run.calls.length, 1);
  assert.equal(run.output, '');
});

for (const [name, patch] of [
  ['HTTP 429', { slackStatus: 429 }], ['HTTP rejection', { slackStatus: 403 }],
  ['unparseable response', { unreadableSlackBody: true }],
  ['Slack rejection', { receipt: { ok: false, error: 'PRIVATE_SLACK_ERROR' } }],
  ['wrong channel', { receipt: { ok: true, channel: 'PRIVATE_SLACK_CHANNEL', ts: '1790961600.123456' } }],
  ['invalid timestamp', { receipt: { ok: true, channel: ROUTING.channel_id, ts: 'PRIVATE_SLACK_TIMESTAMP' } }],
]) {
  test(`notification CLI ${name} leaves intent for reconciliation with no retry or private output`, () => {
    const run = runNotifyCli(patch);
    assert.equal(run.status, 1);
    assert.match(run.stderr, /reconcile/i);
    assert.equal(run.stdout, '');
    assert.equal(run.output, '');
    assert.equal(run.calls.filter(call => call.url.startsWith('https://slack.com/')).length, 1);
    assert.deepEqual(run.calls[2].payload.labels, ['triage/alert-requested']);
    assert.equal(run.stderr.includes('PRIVATE_SLACK'), false);
    assert.equal(run.stderr.includes('1790961600.123456'), false);
  });
}

test('unsupported CLI mode is refused without API access', () => {
  const run = runNotifyCli({ mode: 'unsupported' });
  assert.equal(run.status, 1);
  assert.deepEqual(run.calls, []);
  assert.equal(run.output, '');
});
