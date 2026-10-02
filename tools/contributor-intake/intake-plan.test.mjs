import test from 'node:test';
import assert from 'node:assert/strict';
import { planIntake, validateRouting } from './intake-plan.mjs';

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
    evaluated_at: NOW, routing: ROUTING, item: item(), reviewed_decisions: [], ...overrides,
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

test('missing reviews and forged NVIDIA email stay unverified and visible', () => {
  const result = planIntake(input());
  assert.equal(result.affiliation, 'unverified');
  assert.equal(result.alert_eligible, true);
  assert.equal(result.alert_request_needed, true);
  assert.deepEqual(result.label_changes, {
    remove: ['affiliation/external'], add: ['affiliation/unverified'],
  });
  assert.deepEqual(result.diagnostics, []);
  assert.deepEqual(Object.keys(result).sort(), ['affiliation', 'alert_eligible', 'alert_request_needed',
    'diagnostics', 'input_error', 'label_changes', 'message_text']);
});

test('current numeric-ID NVIDIA review suppresses the opening alert', () => {
  const result = planIntake(input({ reviewed_decisions: [review({ affiliation: 'nvidia' })] }));
  assert.equal(result.affiliation, 'nvidia');
  assert.equal(result.alert_eligible, false);
  assert.equal(result.message_text, null);
});

test('labels do not lock derived affiliation and unrelated metadata survives', () => {
  const data = input({ item: item({ labels: ['affiliation/nvidia', 'enhancement', 'affiliation/bot'] }) });
  const before = structuredClone(data);
  const result = planIntake(data);
  assert.equal(result.affiliation, 'unverified');
  assert.deepEqual(result.label_changes.remove, ['affiliation/nvidia', 'affiliation/bot']);
  assert.deepEqual(result.label_changes.add, ['affiliation/unverified']);
  assert.deepEqual(data, before);
});

test('current external review remains alert eligible', () => {
  const result = planIntake(input({ reviewed_decisions: [review()] }));
  assert.equal(result.affiliation, 'external');
  assert.equal(result.alert_request_needed, true);
});

test('expired review cannot establish external status', () => {
  assert.equal(planIntake(input({ reviewed_decisions: [review({ expires_at: NOW })] })).affiliation, 'unverified');
});

test('multiple current revisions force unresolved review', () => {
  const data = input({ reviewed_decisions: [review(), review({ affiliation: 'nvidia', revision: 'review-2' })] });
  const result = planIntake(data);
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
    assert.equal(planIntake(data).affiliation, 'unverified');
  });
}

test('reused login with mismatched stable author ID forces review', () => {
  const data = input({ reviewed_decisions: [review({ author_id: 88 })] });
  const result = planIntake(data);
  assert.equal(result.affiliation, 'unverified');
  assert.ok(result.diagnostics.includes('review_identity_mismatch'));
});

test('matching stable ID survives a valid login rename', () => {
  const result = planIntake(input({ reviewed_decisions: [review({ login: 'previous-login' })] }));
  assert.equal(result.affiliation, 'external');
  assert.ok(result.diagnostics.includes('review_login_changed'));
});

test('unrelated malformed review does not contaminate another reviewed author', () => {
  const data = input({ reviewed_decisions: [review({ affiliation: 'nvidia' }),
    review({ author_id: 99, login: 'another-user', expires_at: null })] });
  assert.equal(planIntake(data).affiliation, 'nvidia');
});

for (const [name, user] of [
  ['missing', undefined], ['null', null],
  ['ghost', { id: 2003, login: 'ghost', type: 'User' }],
  ['invalid login', { id: 2003, login: '<!here>', type: 'User' }],
  ['unknown type', { id: 2003, login: 'outside-user', type: 'Organization' }],
]) {
  test(`${name} author remains visible with safe fallback`, () => {
    const result = planIntake(input({ item: item({ user }) }));
    assert.equal(result.affiliation, 'unverified');
    assert.ok(result.message_text.includes('Author: author unavailable'));
    assert.equal(result.alert_eligible, true);
  });
}

test('actual contribution Bot suppresses; labeling sender does not decide author identity', () => {
  const bot = planIntake(input({ item: item({ user: { id: 100, login: 'dependabot[bot]', type: 'Bot' } }) }));
  assert.equal(bot.affiliation, 'bot'); assert.equal(bot.alert_eligible, false);
  const human = planIntake(input({ item: item({ sender: { type: 'Bot' } }) }));
  assert.equal(human.affiliation, 'unverified'); assert.equal(human.alert_eligible, true);
});

test('draft and recovered closed PRs retain lifecycle without mutating creation time', () => {
  const data = input({ item: item({ kind: 'pr', state: 'closed', draft: true }) });
  const result = planIntake(data);
  assert.ok(result.message_text.includes('PR #23 (affiliation unverified; already closed; draft)'));
  assert.equal(data.item.created_at, '2026-09-24T12:00:00Z');
  assert.ok(result.message_text.includes('/pull/23|Open GitHub item>'));
});

test('message contains only configured mentions and canonical link; hostile title/company omitted', () => {
  const result = planIntake(input({ reviewed_decisions: [review({ company: '<!everyone> https://evil.invalid' })] }));
  const text = result.message_text;
  assert.equal((text.match(/<@/g) ?? []).length, 2);
  assert.ok(text.includes('<@U0123456789> <@U9876543210>'));
  assert.ok(text.includes('https://github.com/dsx-ai-factory/health-validation/issues/23'));
  assert.equal(text.includes('evil.invalid'), false);
  assert.equal(text.includes('<!'), false);
});

test('existing request requires reconciliation without another alert intent', () => {
  const marked = planIntake(input({ item: item({ labels: ['bug', 'affiliation/unverified', 'triage/alert-requested'] }), reviewed_decisions: [review()] }));
  assert.equal(marked.affiliation, 'external');
  assert.equal(marked.alert_eligible, true);
  assert.equal(marked.alert_request_needed, false);
  assert.ok(marked.diagnostics.includes('existing_request_requires_delivery_reconciliation'));
  assert.equal(marked.label_changes.remove.includes('triage/alert-requested'), false);
  assert.equal(marked.label_changes.add.includes('triage/alert-requested'), false);
});

test('invalid evaluation timestamp fails visibly', () => {
  assert.throws(() => planIntake(input({ evaluated_at: '2026-02-31T05:00:00Z' })));
});

test('review and evaluation timestamps accept microsecond precision', () => {
  const data = input({ evaluated_at: '2026-10-02T05:01:46.197773+00:00',
    reviewed_decisions: [review({ affiliation: 'nvidia', reviewed_at: '2026-10-01T08:41:24.750618+00:00' })] });
  assert.equal(planIntake(data).affiliation, 'nvidia');
});

for (const nodeId of ['MDU6SXNzdWUxMjM=', 'MDExOlB1bGxSZXF1ZXN0MQ==']) {
  test(`opaque legacy GitHub node ID ${nodeId} remains usable`, () => {
    const result = planIntake(input({ item: item({ node_id: nodeId }) }));
    assert.deepEqual(result.input_error, []);
    assert.equal(result.alert_request_needed, true);
  });
}

for (const user of [{ type: 'Bot' }, { type: 'Bot', id: 0, login: 'unknown' }]) {
  test(`known Bot account type with incomplete identity ${JSON.stringify(user)} remains excluded`, () => {
    const result = planIntake(input({ item: item({ user }) }));
    assert.equal(result.affiliation, 'bot');
    assert.equal(result.alert_request_needed, false);
    assert.equal(result.message_text, null);
  });
}

test('fractional User ID remains unverified with an unavailable-author message', () => {
  const result = planIntake(input({ item: item({ user: { id: 1.5, login: 'outside-user', type: 'User' } }) }));
  assert.equal(result.affiliation, 'unverified');
  assert.ok(result.message_text.includes('Author: author unavailable'));
});

for (const [name, patch] of [
  ['missing node ID', { node_id: undefined }],
  ['unsupported kind', { kind: 'merge_request' }],
  ['invalid creation date', { created_at: '2026-02-31T05:00:00Z' }],
  ['missing labels', { labels: undefined }],
  ['invalid lifecycle', { state: 'merged' }],
  ['nonboolean PR draft', { kind: 'pr', draft: 'false' }],
  ['invalid number', { number: '58><!here>' }],
  ['wrong repository', { repository: { id: 1295779810, full_name: 'other/repo' } }],
]) {
  test(`${name} is a visible input error without proposed writes`, () => {
    const result = planIntake(input({ item: item(patch) }));
    assert.ok(result.input_error.length > 0);
    assert.equal(result.alert_eligible, false);
    assert.deepEqual(result.label_changes, { remove: [], add: [] });
    assert.equal(result.message_text, null);
  });
}

test('malformed reviewed collection preserves unknown intake', () => {
  const result = planIntake(input({ reviewed_decisions: {} }));
  assert.equal(result.affiliation, 'unverified');
  assert.equal(result.alert_eligible, true);
  assert.ok(result.diagnostics.includes('reviewed_decisions_malformed'));
});

test('a sparse label array remains an invalid input without proposed writes', () => {
  const result = planIntake(input({ item: item({ labels: Array(1) }) }));
  assert.ok(result.input_error.includes('invalid_labels'));
  assert.equal(result.alert_eligible, false);
  assert.deepEqual(result.label_changes, { remove: [], add: [] });
  assert.equal(result.message_text, null);
});

test('planner requires routing and emits only its trusted recipients', () => {
  const data = input(); delete data.routing;
  assert.throws(() => planIntake(data), /routing/i);
  const result = planIntake(input());
  assert.match(result.message_text, /<@U0123456789> <@U9876543210>/);
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
  assert.deepEqual(validateRouting(one), one);
  const ten = { channel_id: 'C9876543210', mention_user_ids: Array.from({ length: 10 }, (_, i) => `U${String(i).padStart(9, '0')}`) };
  const validated = validateRouting(ten);
  assert.deepEqual(validated, ten);
  validated.mention_user_ids.pop();
  assert.equal(ten.mention_user_ids.length, 10);
});
