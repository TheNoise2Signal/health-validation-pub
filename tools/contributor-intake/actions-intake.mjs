import { readFileSync, appendFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { planIntake, validateRouting } from './intake-plan.mjs';
export { validateRouting } from './intake-plan.mjs';

const REPO = { id: 1295779810, full_name: 'dsx-ai-factory/health-validation' };
const INTENT = 'triage/alert-requested';
const AFFILIATIONS = new Set(['nvidia', 'external', 'unverified', 'bot'].map(value => `affiliation/${value}`));
const record = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const number = value => Number.isSafeInteger(value) && value > 0;

function scope(repository) {
  if (!record(repository) || repository.id !== REPO.id || repository.full_name !== REPO.full_name) throw new Error('repository outside intake scope');
}

function normalize(raw, kind) {
  if (!record(raw)) throw new Error('missing item metadata');
  if (kind === 'pr' && raw.base?.repo) scope(raw.base.repo);
  return { repository: { ...REPO }, node_id: raw.node_id, number: raw.number, kind,
    state: raw.state, draft: kind === 'pr' ? raw.draft : false,
    created_at: raw.created_at, updated_at: raw.updated_at, user: raw.user ?? null,
    labels: Array.isArray(raw.labels) ? raw.labels.map(label => typeof label === 'string' ? label : label?.name) : raw.labels,
    assignees: raw.assignees };
}

/** Trusted Actions event input only. No head SHA, PR branch or contribution text is executed. */
export function openingFromActions(eventName, payload, routing) {
  const route = validateRouting(routing);
  if (!['issues', 'pull_request_target'].includes(eventName) || payload?.action !== 'opened') return null;
  scope(payload.repository);
  const kind = eventName === 'issues' ? 'issue' : 'pr';
  const item = normalize(kind === 'pr' ? payload.pull_request : payload.issue, kind);
  const decision = planIntake({ evaluated_at: item.created_at, items: [item], routing: route }).decisions[0];
  if (decision.input_error.length) throw new Error('invalid opening metadata');
  return item;
}

/** Run under a single per-item Actions concurrency group. An intent is never a receipt. */
export async function prepareActionsOpening({ eventName, payload, evaluatedAt, routing, reviewedDecisions = [] }, github) {
  const route = validateRouting(routing);
  const eventItem = openingFromActions(eventName, payload, route);
  if (!eventItem) return { status: 'ignored', payload: null, delivered: false };
  const current = normalize(await github.readItem(eventItem.kind, eventItem.number), eventItem.kind);
  if (current.node_id !== eventItem.node_id || current.number !== eventItem.number) throw new Error('authoritative item identity mismatch');
  const decision = planIntake({ evaluated_at: evaluatedAt, items: [current], routing: route,
    reviewed_decisions: reviewedDecisions }).decisions[0];
  if (decision.input_error.length) throw new Error('invalid authoritative item metadata');
  if (decision.label_changes.add.length) await github.addLabels(current.number, decision.label_changes.add);
  for (const label of decision.label_changes.remove) await github.removeLabel(current.number, label);
  if (!decision.alert_eligible) return { status: 'suppressed', decision, payload: null, delivered: false };
  if (!decision.alert_request_needed) return { status: 'reconcile', decision, payload: null, delivered: false };
  await github.addLabels(current.number, [INTENT]);
  return { status: 'send', decision, delivered: false,
    payload: { channel: route.channel_id, text: decision.message_preview.text, unfurl_links: false, unfurl_media: false } };
}

export function validateSlackReceipt(receipt, routing) {
  const route = validateRouting(routing);
  if (!record(receipt) || ![true, 'true'].includes(receipt.ok) || receipt.channel_id !== route.channel_id ||
      typeof receipt.ts !== 'string' || !/^\d{1,20}\.\d{1,10}$/.test(receipt.ts)) throw new Error('Slack receipt unverified; reconcile intent before retry');
  return { channel_id: receipt.channel_id, ts: receipt.ts };
}

/** Fixed-scope GitHub transport. No blanket label replacement, redirects or automatic retries. */
export function createGitHubClient({ token, fetchFn = globalThis.fetch }) {
  if (typeof token !== 'string' || !token.trim()) throw new Error('GitHub credential unavailable');
  const root = `https://api.github.com/repos/${REPO.full_name}`;
  const alreadyAbsent = Symbol('label already absent');
  const labelArray = value => Array.isArray(value) && value.every(label => record(label) && typeof label.name === 'string' && label.name.trim());
  const checkedNumber = value => { if (!number(value)) throw new Error('invalid item number'); return value; };
  async function request(path, method = 'GET', body) {
    let response;
    try {
      response = await fetchFn(root + path, { method, redirect: 'error', signal: AbortSignal.timeout(10000),
        headers: { Authorization: `Bearer ${token}`, Accept: 'application/vnd.github+json',
          'Content-Type': 'application/json', 'X-GitHub-Api-Version': '2022-11-28' },
        body: body === undefined ? undefined : JSON.stringify(body) });
    } catch { throw new Error('GitHub request unavailable; reconcile any attempted write'); }
    if (method === 'DELETE' && response.status === 404) return alreadyAbsent;
    if (!response.ok) throw new Error(`GitHub request failed (${response.status})`);
    try { return await response.json(); } catch { throw new Error('GitHub response invalid; reconcile any attempted write'); }
  }
  return {
    async readItem(kind, itemNumber) {
      if (!['issue', 'pr'].includes(kind)) throw new Error('invalid item kind');
      return request(`/${kind === 'pr' ? 'pulls' : 'issues'}/${checkedNumber(itemNumber)}`);
    },
    async addLabels(itemNumber, labels) {
      if (!Array.isArray(labels) || !labels.length || !labels.every(label => AFFILIATIONS.has(label) || label === INTENT)) throw new Error('label outside intake scope');
      const response = await request(`/issues/${checkedNumber(itemNumber)}/labels`, 'POST', { labels });
      if (!labelArray(response) ||
          !labels.every(name => response.some(label => label.name === name))) {
        throw new Error('GitHub label write unverified; reconcile attempted write');
      }
      return response;
    },
    async removeLabel(itemNumber, label) {
      if (!AFFILIATIONS.has(label)) throw new Error('label outside intake scope');
      const response = await request(`/issues/${checkedNumber(itemNumber)}/labels/${encodeURIComponent(label)}`, 'DELETE');
      if (response === alreadyAbsent) return null;
      if (!labelArray(response) || response.some(current => current.name === label)) {
        throw new Error('GitHub label removal unverified; reconcile attempted write');
      }
      return response;
    },
  };
}

function routingFromEnvironment() {
  let mentions;
  try { mentions = JSON.parse(process.env.HEALTH_VALIDATION_INTAKE_MENTION_USER_IDS_JSON); }
  catch { throw new Error('invalid Slack routing configuration'); }
  return validateRouting({ channel_id: process.env.HEALTH_VALIDATION_INTAKE_CHANNEL, mention_user_ids: mentions });
}

const warnEvidence = () => process.stderr.write('Affiliation evidence unavailable or malformed; unresolved authors remain unverified.\n');

async function cli() {
  const mode = process.argv[2];
  const routing = routingFromEnvironment();
  if (mode === 'validate-config') {
    process.stdout.write('Routing configuration validated.\n');
    return;
  }
  if (mode === 'validate-receipt') {
    validateSlackReceipt({ ok: process.env.HEALTH_INTAKE_SLACK_OK,
      channel_id: process.env.HEALTH_INTAKE_SLACK_CHANNEL, ts: process.env.HEALTH_INTAKE_SLACK_TS }, routing);
    process.stdout.write('Receipt validated. Record the item and receipt in the private pilot journal.\n');
    return;
  }
  if (mode !== 'prepare') throw new Error('use validate-config, prepare or validate-receipt');
  if (process.env.HEALTH_VALIDATION_APPLY_LABELS !== 'true') throw new Error('label application is disabled');
  if (!process.env.GITHUB_OUTPUT?.trim()) throw new Error('Actions output unavailable');
  const eventPath = process.env.GITHUB_EVENT_PATH;
  if (!eventPath) throw new Error('Actions event unavailable');
  let payload;
  try { payload = JSON.parse(readFileSync(eventPath, 'utf8')); } catch { throw new Error('Actions event invalid'); }
  let reviewedDecisions = [], evidenceWarning = false;
  try {
    const parsed = JSON.parse(process.env.HEALTH_VALIDATION_REVIEWED_DECISIONS_JSON);
    if (!Array.isArray(parsed)) throw new Error('reviewed evidence must be an array');
    reviewedDecisions = parsed;
  } catch { evidenceWarning = true; }
  if (evidenceWarning) warnEvidence();
  const result = await prepareActionsOpening({ eventName: process.env.GITHUB_EVENT_NAME, payload,
    evaluatedAt: new Date().toISOString(), routing, reviewedDecisions }, createGitHubClient({ token: process.env.GITHUB_TOKEN }));
  if (!evidenceWarning && result.decision?.diagnostics.includes('review_malformed')) warnEvidence();
  appendFileSync(process.env.GITHUB_OUTPUT, `status=${result.status}\nsend=${result.status === 'send'}\npayload=${JSON.stringify(result.payload)}\n`);
  process.stdout.write(`Intake preparation: ${result.status}. No Slack delivery claimed.\n`);
  if (result.status === 'reconcile') process.exitCode = 2;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  cli().catch(() => { process.stderr.write('Intake step failed. Check permissions/configuration; reconcile any intent before retry.\n'); process.exitCode = 1; });
}
