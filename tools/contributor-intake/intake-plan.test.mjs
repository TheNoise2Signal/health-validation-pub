import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { planIntake } from './intake-plan.mjs';
import * as intake from './intake-plan.mjs';

const NODE_RUNTIME_KEYS = ['JS_BINARY__NODE_BINARY', 'JS_BINARY__NODE_PATCHES', 'JS_BINARY__NODE_WRAPPER',
  'JS_BINARY__PATCH_NODE_FS', 'JS_BINARY__FS_PATCH_ROOTS'];
const nodeRuntimeEnvironment = () => Object.fromEntries(NODE_RUNTIME_KEYS
  .filter(key => typeof process.env[key] === 'string').map(key => [key, process.env[key]]));

const ROUTING = { channel_id: 'C0123456789', mention_user_ids: ['U0123456789', 'U9876543210'] };
const NOW = '2026-10-02T05:00:00Z';
function item(overrides = {}) {
  return {
    repository: { id: 1295779810, full_name: 'dsx-ai-factory/health-validation' },
    node_id: 'I_TEST_1', number: 23, kind: 'issue', state: 'open', draft: false,
    created_at: '2026-09-24T12:00:00Z',
    user: { id: 1001, login: 'outside-user', type: 'User' },
    labels: ['bug', 'affiliation/external'], assignees: ['maintainer'],
    title: '<!here> <@U_BAD> <https://evil.invalid|click>',
    url: 'https://evil.invalid/23', author_association: 'COLLABORATOR',
    commit_email: 'forged@nvidia.com', ...overrides,
  };
}
function input(overrides = {}) {
  return {
    evaluated_at: NOW, routing: ROUTING, items: [item()], reviewed_decisions: [], ...overrides,
  };
}
function review(overrides = {}) {
  return {
    author_id: 1001, login: 'outside-user', affiliation: 'external',
    reviewed_by: 'reviewer', evidence_ref: 'https://github.com/example/evidence',
    reviewed_at: '2026-10-01T12:00:00Z', expires_at: '2026-10-10T12:00:00Z',
    revision: 'review-1', ...overrides,
  };
}
const first = data => planIntake(data).decisions[0];

test('registry-only unknown intake has no automatic-source dependency', () => {
  const result = first(input());
  assert.equal(result.affiliation, 'unverified');
  assert.equal(result.alert_eligible, true);
  assert.deepEqual(result.diagnostics, []);
});

test('missing reviews and forged NVIDIA email stay unverified and visible', () => {
  const result = first(input());
  assert.equal(result.affiliation, 'unverified');
  assert.equal(result.alert_eligible, true);
  assert.equal(result.alert_request_needed, true);
  assert.deepEqual(result.label_changes, {
    remove: ['affiliation/external'], add: ['affiliation/unverified'],
  });
  assert.deepEqual(result.labels_after, ['bug', 'affiliation/unverified']);
  assert.deepEqual(result.assignees, ['maintainer']);
  assert.equal(result.delivered, false);
});

test('current numeric-ID NVIDIA review suppresses the opening alert', () => {
  const result = first(input({ reviewed_decisions: [review({ affiliation: 'nvidia' })] }));
  assert.equal(result.affiliation, 'nvidia');
  assert.equal(result.alert_eligible, false);
  assert.equal(result.message_preview, null);
});

test('labels do not lock derived affiliation and unrelated metadata survives', () => {
  const data = input({ items: [item({ labels: ['affiliation/nvidia', 'enhancement', 'affiliation/bot'] })] });
  const before = structuredClone(data);
  const result = first(data);
  assert.equal(result.affiliation, 'unverified');
  assert.deepEqual(result.label_changes.remove, ['affiliation/nvidia', 'affiliation/bot']);
  assert.deepEqual(result.labels_after, ['enhancement', 'affiliation/unverified']);
  assert.deepEqual(data, before);
});

test('current external review remains alert eligible', () => {
  const result = first(input({ reviewed_decisions: [review()] }));
  assert.equal(result.affiliation, 'external');
  assert.equal(result.alert_request_needed, true);
});

test('expired review cannot establish external status', () => {
  assert.equal(first(input({ reviewed_decisions: [review({ expires_at: NOW })] })).affiliation, 'unverified');
});

test('multiple current revisions force unresolved review', () => {
  const data = input({ reviewed_decisions: [review(), review({ affiliation: 'nvidia', revision: 'review-2' })] });
  const result = first(data);
  assert.equal(result.affiliation, 'unverified');
  assert.ok(result.diagnostics.includes('review_conflict'));
});

for (const [name, changes] of [
  ['missing expiry', { expires_at: undefined }],
  ['missing reviewer', { reviewed_by: '' }],
  ['invalid affiliation', { affiliation: 'internal' }],
  ['future review', { reviewed_at: '2026-10-03T05:00:00Z' }],
  ['invalid date', { reviewed_at: '2026-02-31T05:00:00Z' }],
]) {
  test(`relevant malformed review (${name}) cannot suppress`, () => {
    const data = input({ reviewed_decisions: [review(changes)] });
    assert.equal(first(data).affiliation, 'unverified');
  });
}

test('reused login with mismatched stable author ID forces review', () => {
  const data = input({ reviewed_decisions: [review({ author_id: 88 })] });
  const result = first(data);
  assert.equal(result.affiliation, 'unverified');
  assert.ok(result.diagnostics.includes('review_identity_mismatch'));
});

test('matching stable ID survives a valid login rename', () => {
  const result = first(input({ reviewed_decisions: [review({ login: 'previous-login' })] }));
  assert.equal(result.affiliation, 'external');
  assert.ok(result.diagnostics.includes('review_login_changed'));
});

test('unrelated malformed review does not contaminate another reviewed author', () => {
  const data = input({ reviewed_decisions: [review({ affiliation: 'nvidia' }),
    review({ author_id: 99, login: 'another-user', expires_at: null })] });
  assert.equal(first(data).affiliation, 'nvidia');
});

for (const [name, user] of [
  ['missing', undefined], ['null', null],
  ['ghost', { id: 2003, login: 'ghost', type: 'User' }],
  ['invalid login', { id: 2003, login: '<!here>', type: 'User' }],
  ['unknown type', { id: 2003, login: 'outside-user', type: 'Organization' }],
]) {
  test(`${name} author remains visible with safe fallback`, () => {
    const result = first(input({ items: [item({ user })] }));
    assert.equal(result.affiliation, 'unverified');
    assert.ok(result.message_preview.text.includes('Author: author unavailable'));
    assert.equal(result.alert_eligible, true);
  });
}

test('actual contribution Bot suppresses; labeling sender does not decide author identity', () => {
  const bot = first(input({ items: [item({ user: { id: 100, login: 'dependabot[bot]', type: 'Bot' } })] }));
  assert.equal(bot.affiliation, 'bot'); assert.equal(bot.alert_eligible, false);
  const human = first(input({ items: [item({ sender: { type: 'Bot' } })] }));
  assert.equal(human.affiliation, 'unverified'); assert.equal(human.alert_eligible, true);
});

test('draft and recovered closed PRs retain lifecycle and original creation time', () => {
  const result = first(input({ items: [item({ kind: 'pr', state: 'closed', draft: true })] }));
  assert.ok(result.message_preview.text.includes('PR #23 (affiliation unverified; already closed; draft)'));
  assert.equal(result.created_at, '2026-09-24T12:00:00Z');
  assert.ok(result.message_preview.text.includes('/pull/23|Open GitHub item>'));
});

test('preview contains only configured mentions and canonical link; hostile title/company omitted', () => {
  const result = first(input({ reviewed_decisions: [review({ company: '<!everyone> https://evil.invalid' })] }));
  const text = result.message_preview.text;
  assert.equal((text.match(/<@/g) ?? []).length, 2);
  assert.ok(text.includes('<@U0123456789> <@U9876543210>'));
  assert.ok(text.includes('https://github.com/dsx-ai-factory/health-validation/issues/23'));
  assert.equal(text.includes('evil.invalid'), false);
  assert.equal(text.includes('<!'), false);
  assert.equal(result.message_preview.delivered, false);
});

test('request ordering is explicit and existing request is not a receipt or retry', () => {
  const fresh = first(input());
  assert.deepEqual(fresh.proposed_steps.map(step => step.kind), ['reconcile_affiliation_labels', 'request_alert']);
  const marked = first(input({ items: [item({ labels: ['bug', 'affiliation/unverified', 'triage/alert-requested'] })], reviewed_decisions: [review()] }));
  assert.equal(marked.affiliation, 'external');
  assert.equal(marked.alert_eligible, true);
  assert.equal(marked.alert_request_needed, false);
  assert.equal(marked.delivered, false);
  assert.ok(marked.diagnostics.includes('existing_request_requires_delivery_reconciliation'));
  assert.ok(marked.labels_after.includes('triage/alert-requested'));
  assert.equal(marked.proposed_steps.some(step => step.kind === 'request_alert'), false);
});

test('repeated inputs produce same key without claiming durable duplicate protection', () => {
  const firstRun = planIntake(input()); const secondRun = planIntake(input());
  assert.deepEqual(firstRun, secondRun);
  assert.equal(firstRun.durable_deduplication, false);
  assert.equal(firstRun.read_only, true);
});

test('invalid rows remain visible and counts reconcile', () => {
  const result = planIntake(input({ items: [item(), item({ number: '58><!here>' }), item({ repository: { id: 1295779810, full_name: 'other/repo' } })] }));
  assert.equal(result.decisions.length, 3);
  assert.equal(result.counts.input, 3);
  assert.equal(result.counts.invalid, 2);
  assert.equal(result.counts.classified, 1);
  assert.equal(result.decisions[1].input_error.length > 0, true);
  assert.equal(result.decisions[1].alert_eligible, false);
  assert.equal(result.decisions[1].proposed_steps.length, 0);
});

test('invalid top-level clock and item collection fail visibly', () => {
  assert.throws(() => planIntake(input({ evaluated_at: '2026-02-31T05:00:00Z' })));
  assert.throws(() => planIntake(input({ items: null })));
});

test('review and evaluation timestamps accept microsecond precision', () => {
  const data = input({ evaluated_at: '2026-10-02T05:01:46.197773+00:00',
    reviewed_decisions: [review({ affiliation: 'nvidia', reviewed_at: '2026-10-01T08:41:24.750618+00:00' })] });
  assert.equal(first(data).affiliation, 'nvidia');
});

test('duplicate identities remain visible without duplicate request plans', () => {
  const result = planIntake(input({ items: [item(), item()] }));
  assert.equal(result.decisions.length, 2);
  assert.ok(result.counts.invalid >= 1);
  assert.ok(result.decisions.filter(row => row.alert_request_needed).length <= 1);
  assert.ok(result.decisions.some(row => row.input_error.length > 0));
});

for (const nodeId of ['MDU6SXNzdWUxMjM=', 'MDExOlB1bGxSZXF1ZXN0MQ==']) {
  test(`opaque legacy GitHub node ID ${nodeId} remains usable`, () => {
    const result = first(input({ items: [item({ node_id: nodeId })] }));
    assert.deepEqual(result.input_error, []);
    assert.equal(JSON.parse(result.notification_key)[1], nodeId);
    assert.equal(result.alert_request_needed, true);
  });
}

test('issue and PR share one repository number namespace for duplicate detection', () => {
  const result = planIntake(input({ items: [item({ node_id: 'I_FIRST' }), item({ node_id: 'PR_SECOND', kind: 'pr' })] }));
  assert.equal(result.counts.input, 2);
  assert.equal(result.counts.invalid, 2);
  assert.equal(result.counts.alert_request_needed, 0);
});

for (const user of [{ type: 'Bot' }, { type: 'Bot', id: 0, login: 'unknown' }]) {
  test(`known Bot account type with incomplete identity ${JSON.stringify(user)} remains excluded`, () => {
    const result = first(input({ items: [item({ user })] }));
    assert.equal(result.affiliation, 'bot');
    assert.equal(result.alert_request_needed, false);
    assert.equal(result.message_preview, null);
  });
}

test('fractional User ID remains unverified with an unavailable-author preview', () => {
  const result = first(input({ items: [item({ user: { id: 1.5, login: 'outside-user', type: 'User' } })] }));
  assert.equal(result.affiliation, 'unverified');
  assert.ok(result.message_preview.text.includes('Author: author unavailable'));
});

for (const [name, patch] of [
  ['missing node ID', { node_id: undefined }],
  ['unsupported kind', { kind: 'merge_request' }],
  ['invalid creation date', { created_at: '2026-02-31T05:00:00Z' }],
  ['missing labels', { labels: undefined }],
  ['invalid lifecycle', { state: 'merged' }],
  ['nonboolean PR draft', { kind: 'pr', draft: 'false' }],
  ['invalid assignment collection', { assignees: 'maintainer' }],
]) {
  test(`${name} is a visible row error rather than a mutation plan`, () => {
    const result = first(input({ items: [item(patch)] }));
    assert.ok(result.input_error.length > 0);
    assert.equal(result.alert_eligible, false);
    assert.deepEqual(result.proposed_steps, []);
  });
}

const cli = fileURLToPath(new URL('./intake-plan.mjs', import.meta.url));
function runCli(args, body) {
  return spawnSync(process.execPath, [cli, ...args], {
    encoding: 'utf8', input: body, env: { ...nodeRuntimeEnvironment(), PATH: process.env.PATH }, timeout: 5000,
  });
}

test('CLI accepts stdin and returns a read-only plan', () => {
  const run = runCli(['--input', '-'], JSON.stringify(input()));
  assert.equal(run.status, 0, run.stderr);
  const result = JSON.parse(run.stdout);
  assert.equal(result.read_only, true);
  assert.equal(result.counts.input, 1);
  assert.equal(result.decisions[0].delivered, false);
});

test('CLI accepts a local file and leaves it unchanged', () => {
  const directory = mkdtempSync(join(tmpdir(), 'health-intake-cli-'));
  try {
    const path = join(directory, 'input.json');
    const serialized = JSON.stringify(input()); writeFileSync(path, serialized);
    const run = runCli(['--input', path]);
    assert.equal(run.status, 0, run.stderr);
    assert.equal(JSON.parse(run.stdout).counts.input, 1);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test('CLI returns partial plan and exit 2 for invalid rows', () => {
  const run = runCli(['--input', '-'], JSON.stringify(input({ items: [item({ number: null })] })));
  assert.equal(run.status, 2, run.stderr);
  assert.equal(JSON.parse(run.stdout).counts.invalid, 1);
});

test('CLI rejects malformed JSON without leaking the supplied payload', () => {
  const run = runCli(['--input', '-'], 'PRIVATE_PAYLOAD invalid JSON');
  assert.equal(run.status, 1);
  assert.equal(run.stderr.includes('PRIVATE_PAYLOAD'), false);
  assert.equal(run.stdout, '');
});

test('CLI rejects an unreadable file without leaking its private path', () => {
  const directory = mkdtempSync(join(tmpdir(), 'health-intake-cli-'));
  try {
    const run = runCli(['--input', join(directory, 'PRIVATE_INPUT_PATH.json')]);
    assert.equal(run.status, 1);
    assert.equal(run.stdout, '');
    assert.equal(run.stderr.includes('PRIVATE_INPUT_PATH'), false);
    assert.equal(run.stderr.includes(directory), false);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test('CLI refuses apply mode and reports missing input', () => {
  for (const args of [[], ['--apply'], ['--input', '-', '--apply']]) {
    const run = runCli(args, JSON.stringify(input()));
    assert.equal(run.status, 1);
    assert.equal(run.stdout, '');
  }
});

test('malformed reviewed collection preserves unknown intake', () => {
  const result = first(input({ reviewed_decisions: {} }));
  assert.equal(result.affiliation, 'unverified');
  assert.equal(result.alert_eligible, true);
  assert.ok(result.diagnostics.includes('reviewed_decisions_malformed'));
});

test('planner requires routing and emits only its trusted recipients and channel', () => {
  const data = input(); delete data.routing;
  assert.throws(() => planIntake(data), /routing/i);
  const result = first(input());
  assert.equal(result.message_preview.channel_id, 'C0123456789');
  assert.match(result.message_preview.text, /<@U0123456789> <@U9876543210>/);
  assert.equal(JSON.parse(result.notification_key)[2], 'C0123456789');
});

for (const [name, routing] of [
  ['missing', undefined], ['null', null], ['text', 'C0123456789'],
  ['invalid channel', { ...ROUTING, channel_id: 'general' }],
  ['short channel', { ...ROUTING, channel_id: 'C1' }],
  ['channel whitespace', { ...ROUTING, channel_id: ' C0123456789' }],
  ['channel syntax', { ...ROUTING, channel_id: 'C0123456789><!everyone>' }],
  ['missing mentions', { channel_id: 'C0123456789' }],
  ['empty mentions', { ...ROUTING, mention_user_ids: [] }],
  ['sparse mentions', { ...ROUTING, mention_user_ids: Array(1) }],
  ['mention text', { ...ROUTING, mention_user_ids: ['@here'] }],
  ['mention syntax', { ...ROUTING, mention_user_ids: ['U0123456789> <!everyone>'] }],
  ['mention whitespace', { ...ROUTING, mention_user_ids: [' U0123456789'] }],
  ['duplicate mentions', { ...ROUTING, mention_user_ids: ['U0123456789', 'U0123456789'] }],
  ['too many mentions', { ...ROUTING, mention_user_ids: Array.from({ length: 11 }, (_, i) => `U${String(i).padStart(9, '0')}`) }],
]) {
  test(`invalid routing (${name}) is rejected`, () => {
    assert.throws(() => planIntake(input({ routing })), /routing/i);
  });
}

test('routing validator accepts one through ten opaque unique recipients and copies input', () => {
  const one = { channel_id: 'C0123456789', mention_user_ids: ['U0123456789'] };
  assert.deepEqual(intake.validateRouting(one), one);
  const ten = { channel_id: 'C9876543210', mention_user_ids: Array.from({ length: 10 }, (_, i) => `U${String(i).padStart(9, '0')}`) };
  const validated = intake.validateRouting(ten);
  assert.deepEqual(validated, ten);
  validated.mention_user_ids.pop();
  assert.equal(ten.mention_user_ids.length, 10);
});
