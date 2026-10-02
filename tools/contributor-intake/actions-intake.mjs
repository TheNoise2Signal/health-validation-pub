import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { request as httpsRequest } from 'node:https';
import { planIntake, validateRouting } from './intake-plan.mjs';

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
    created_at: raw.created_at, user: raw.user ?? null,
    labels: Array.isArray(raw.labels) ? raw.labels.map(label => typeof label === 'string' ? label : label?.name) : raw.labels };
}

/** Trusted Actions event input only. No head SHA, PR branch or contribution text is executed. */
export function openingFromActions(eventName, payload, routing) {
  const route = validateRouting(routing);
  if (!['issues', 'pull_request_target'].includes(eventName) || payload?.action !== 'opened') return null;
  scope(payload.repository);
  const kind = eventName === 'issues' ? 'issue' : 'pr';
  const item = normalize(kind === 'pr' ? payload.pull_request : payload.issue, kind);
  const decision = planIntake({ evaluated_at: item.created_at, item, routing: route });
  if (decision.input_error.length) throw new Error('invalid opening metadata');
  return item;
}

/** Run under a single per-item Actions concurrency group. An intent is never a receipt. */
export async function prepareActionsOpening({ eventName, payload, evaluatedAt, routing, reviewedDecisions = [] }, github) {
  const route = validateRouting(routing);
  const eventItem = openingFromActions(eventName, payload, route);
  if (!eventItem) return { status: 'ignored', payload: null };
  const current = normalize(await github.readItem(eventItem.kind, eventItem.number), eventItem.kind);
  if (current.node_id !== eventItem.node_id || current.number !== eventItem.number) throw new Error('authoritative item identity mismatch');
  const decision = planIntake({ evaluated_at: evaluatedAt, item: current, routing: route,
    reviewed_decisions: reviewedDecisions });
  if (decision.input_error.length) throw new Error('invalid authoritative item metadata');
  if (decision.label_changes.add.length) await github.addLabels(current.number, decision.label_changes.add);
  for (const label of decision.label_changes.remove) await github.removeLabel(current.number, label);
  if (!decision.alert_eligible) return { status: 'suppressed', decision, payload: null };
  if (!decision.alert_request_needed) return { status: 'reconcile', decision, payload: null };
  await github.addLabels(current.number, [INTENT]);
  return { status: 'send', decision,
    payload: { channel: route.channel_id, text: decision.message_text, unfurl_links: false, unfurl_media: false } };
}

function validateSlackReceipt(receipt, routing) {
  const route = validateRouting(routing);
  if (!record(receipt) || receipt.ok !== true || receipt.channel !== route.channel_id ||
      typeof receipt.ts !== 'string' || !/^\d{1,20}\.\d{1,10}$/.test(receipt.ts)) throw new Error('Slack receipt unverified; reconcile intent before retry');
  return { channel_id: receipt.channel, ts: receipt.ts };
}

export function createGitHubClient({ token, fetchFn = globalThis.fetch }) {
  if (typeof token !== 'string' || !token.trim()) throw new Error('GitHub credential unavailable');
  const root = `https://api.github.com/repos/${REPO.full_name}`;
  const alreadyAbsent = Symbol('label already absent');
  const labelArray = value => Array.isArray(value) && Array.from(value).every(label => record(label) && typeof label.name === 'string' && label.name.trim());
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
      if (!Array.isArray(labels) || !labels.length || !Array.from(labels).every(label => AFFILIATIONS.has(label) || label === INTENT)) throw new Error('label outside intake scope');
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

/** One attempt only: an interrupted post can succeed without a readable receipt. */
export function createSlackClient({ token, requestFn = httpsRequest }) {
  if (typeof token !== 'string' || !token.trim()) throw new Error('Slack credential unavailable');
  return {
    async sendNotification(payload, routing) {
      const route = validateRouting(routing);
      if (!record(payload) || payload.channel !== route.channel_id || typeof payload.text !== 'string' || !payload.text.trim() ||
          payload.unfurl_links !== false || payload.unfurl_media !== false) throw new Error('Slack payload invalid');
      const body = JSON.stringify(payload);
      return new Promise((resolve, reject) => {
        let request, response, settled = false, bytes = 0;
        const chunks = [];
        const fail = () => {
          if (settled) return;
          settled = true;
          clearTimeout(deadline);
          reject(new Error('Slack request unverified; reconcile intent before retry'));
          response?.destroy();
          request?.destroy();
        };
        const deadline = setTimeout(fail, 10000);
        try {
          request = requestFn('https://slack.com/api/chat.postMessage', {
            method: 'POST', agent: false,
            headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json',
              'Content-Length': Buffer.byteLength(body) },
          }, incoming => {
            response = incoming;
            response.on('error', fail);
            response.on('aborted', fail);
            response.on('close', () => { if (!settled) fail(); });
            if (settled) { response.destroy(); return; }
            if (!Number.isInteger(response.statusCode) || response.statusCode < 200 || response.statusCode >= 300) { fail(); return; }
            response.on('data', chunk => {
              if (settled) return;
              const buffer = Buffer.from(chunk);
              bytes += buffer.length;
              if (bytes > 65536) { fail(); return; }
              chunks.push(buffer);
            });
            response.on('end', () => {
              if (settled) return;
              let receipt;
              try { receipt = validateSlackReceipt(JSON.parse(Buffer.concat(chunks).toString('utf8')), route); }
              catch { fail(); return; }
              settled = true;
              clearTimeout(deadline);
              resolve(receipt);
            });
          });
          request.on('error', fail);
          request.end(body);
        } catch { fail(); }
      });
    },
  };
}

function routingFromEnvironment() {
  let routing;
  try { routing = JSON.parse(process.env.HEALTH_VALIDATION_INTAKE_ROUTING_JSON); }
  catch { throw new Error('invalid Slack routing configuration'); }
  return validateRouting(routing);
}

const warnEvidence = () => process.stderr.write('Affiliation evidence unavailable or malformed; unresolved authors remain unverified.\n');

async function cli() {
  if (process.argv.length !== 3 || process.argv[2] !== 'notify') throw new Error('use notify');
  const routing = routingFromEnvironment();
  if (process.env.HEALTH_VALIDATION_APPLY_LABELS !== 'true') throw new Error('label application is disabled');
  const github = createGitHubClient({ token: process.env.GITHUB_TOKEN });
  const slack = createSlackClient({ token: process.env.HEALTH_VALIDATION_SLACK_BOT_TOKEN });
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
    evaluatedAt: new Date().toISOString(), routing, reviewedDecisions }, github);
  if (!evidenceWarning && result.decision?.diagnostics.includes('review_malformed')) warnEvidence();
  if (result.status === 'send') {
    await slack.sendNotification(result.payload, routing);
    process.stdout.write('Notification receipt validated. Record it manually in the private pilot journal.\n');
  } else {
    process.stdout.write(`Intake notification: ${result.status}. No Slack post attempted.\n`);
  }
  if (result.status === 'reconcile') process.exitCode = 2;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  cli().catch(() => { process.stderr.write('Intake step failed. Check permissions/configuration; reconcile any intent before retry.\n'); process.exitCode = 1; });
}
