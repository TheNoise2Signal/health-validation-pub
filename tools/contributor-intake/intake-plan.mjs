import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const REPOSITORY_ID = 1295779810;
const REPOSITORY = 'dsx-ai-factory/health-validation';
const REQUEST_LABEL = 'triage/alert-requested';
const MANAGED_LABELS = ['nvidia', 'external', 'unverified', 'bot'].map(value => `affiliation/${value}`);
const isRecord = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const positiveInteger = value => Number.isSafeInteger(value) && value > 0;
const nonempty = value => typeof value === 'string' && value.trim().length > 0;
const validLogin = value => typeof value === 'string' && /^[A-Za-z0-9][A-Za-z0-9-]{0,38}$/.test(value) && value.toLowerCase() !== 'ghost';
const normalizeLogin = value => typeof value === 'string' ? value.toLowerCase() : null;
// GitHub IDs are opaque; legacy IDs may contain padded Base64 characters.
const validNode = value => typeof value === 'string' && value.length > 0 && value.length <= 512 && !/[\s\u0000-\u001f\u007f]/u.test(value);

// Date.parse normalizes impossible calendar dates; verify components first.
function timestamp(value) {
  if (typeof value !== 'string') return null;
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d+))?(Z|[+-]\d{2}:\d{2})$/.exec(value);
  if (!match) return null;
  const [, yearText, monthText, dayText, hourText, minuteText, secondText, , offset] = match;
  const [year, month, day, hour, minute, second] = [yearText, monthText, dayText, hourText, minuteText, secondText].map(Number);
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  if (year < 1 || month < 1 || month > 12 || day < 1 || day > days[month - 1] || hour > 23 || minute > 59 || second > 59) return null;
  if (offset !== 'Z' && (Number(offset.slice(1, 3)) > 23 || Number(offset.slice(4, 6)) > 59)) return null;
  const result = Date.parse(value);
  return Number.isFinite(result) ? result : null;
}

export { timestamp as parseTimestamp };

export function validateRouting(routing) {
  if (!isRecord(routing) || typeof routing.channel_id !== 'string' || !/^C[A-Z0-9]{8,31}$/.test(routing.channel_id) ||
      !Array.isArray(routing.mention_user_ids) || routing.mention_user_ids.length < 1 || routing.mention_user_ids.length > 10 ||
      !Array.from(routing.mention_user_ids).every(id => typeof id === 'string' && /^U[A-Z0-9]{8,31}$/.test(id)) ||
      new Set(routing.mention_user_ids).size !== routing.mention_user_ids.length) {
    throw new TypeError('invalid Slack routing configuration');
  }
  return { channel_id: routing.channel_id, mention_user_ids: [...routing.mention_user_ids] };
}

function authorIdentity(user) {
  if (isRecord(user) && user.type === 'Bot') return { human: false, bot: true, id: user.id, login: user.login };
  if (!isRecord(user) || !positiveInteger(user.id)) return { human: false, bot: false };
  const human = user.type === 'User' && validLogin(user.login);
  return { human, bot: false, id: user.id, login: user.login };
}

function reviewedAffiliation(author, reviews, now, diagnostics) {
  const relevant = reviews.filter(review => isRecord(review) && (
    review.author_id === author.id ||
    (typeof review.author_id === 'string' && review.author_id === String(author.id)) ||
    normalizeLogin(review.login) === normalizeLogin(author.login)
  ));
  const current = [];
  let malformed = false;
  for (const review of relevant) {
    const reviewed = timestamp(review.reviewed_at);
    const expires = timestamp(review.expires_at);
    if (!positiveInteger(review.author_id) || !validLogin(review.login) || !['nvidia', 'external'].includes(review.affiliation) ||
        !nonempty(review.reviewed_by) || !nonempty(review.evidence_ref) || !nonempty(review.revision) ||
        reviewed === null || expires === null || reviewed > now || expires <= reviewed) {
      malformed = true;
    } else if (expires <= now) {
      diagnostics.push('review_expired');
    } else {
      current.push(review);
    }
  }
  if (malformed) {
    diagnostics.push('review_malformed');
    return 'unverified';
  }
  if (current.some(review => review.author_id !== author.id)) {
    diagnostics.push('review_identity_mismatch');
    return 'unverified';
  }
  if (current.length > 1) {
    diagnostics.push('review_conflict');
    return 'unverified';
  }
  if (current.length === 0) return null;
  if (normalizeLogin(current[0].login) !== normalizeLogin(author.login)) diagnostics.push('review_login_changed');
  return current[0].affiliation;
}

function itemErrors(item) {
  if (!isRecord(item)) return ['item_not_object'];
  const errors = [];
  if (!isRecord(item.repository) || item.repository.id !== REPOSITORY_ID || item.repository.full_name !== REPOSITORY) errors.push('repository_out_of_scope');
  if (!validNode(item.node_id)) errors.push('invalid_node_id');
  if (!positiveInteger(item.number)) errors.push('invalid_number');
  if (!['issue', 'pr'].includes(item.kind)) errors.push('invalid_kind');
  if (!['open', 'closed'].includes(item.state)) errors.push('invalid_state');
  if ((item.kind === 'pr' || item.draft !== undefined) && typeof item.draft !== 'boolean') errors.push('invalid_draft');
  if (timestamp(item.created_at) === null) errors.push('invalid_created_at');
  if (!Array.isArray(item.labels) || !Array.from(item.labels).every(nonempty) || new Set(item.labels).size !== item.labels.length) errors.push('invalid_labels');
  if (!Array.isArray(item.assignees)) errors.push('invalid_assignees');
  return errors;
}

function markDuplicateItems(items, errors) {
  const nodes = new Map();
  const numbers = new Map();
  const track = (map, key, index) => {
    if (!map.has(key)) map.set(key, []);
    map.get(key).push(index);
  };
  items.forEach((item, index) => {
    if (!isRecord(item) || errors[index].length) return;
    if (validNode(item.node_id)) track(nodes, item.node_id, index);
    if (isRecord(item.repository) && item.repository.id === REPOSITORY_ID && ['issue', 'pr'].includes(item.kind) && positiveInteger(item.number)) {
      // GitHub issues and PRs share the repository's number namespace.
      track(numbers, `${item.number}`, index);
    }
  });
  for (const group of [...nodes.values(), ...numbers.values()]) {
    if (group.length > 1) for (const index of group) if (!errors[index].includes('duplicate_item')) errors[index].push('duplicate_item');
  }
}

function emptyDecision(item, index, inputError) {
  return {
    input_index: index,
    node_id: validNode(item?.node_id) ? item.node_id : null,
    number: positiveInteger(item?.number) ? item.number : null,
    kind: ['issue', 'pr'].includes(item?.kind) ? item.kind : null,
    state: ['open', 'closed'].includes(item?.state) ? item.state : null,
    draft: typeof item?.draft === 'boolean' ? item.draft : false,
    created_at: timestamp(item?.created_at) !== null ? item.created_at : null,
    input_error: inputError,
    affiliation: null,
    affiliation_source: 'input_error',
    label_changes: { remove: [], add: [] },
    labels_after: Array.isArray(item?.labels) && item.labels.every(nonempty) ? [...item.labels] : [],
    assignees: Array.isArray(item?.assignees) ? structuredClone(item.assignees) : [],
    alert_eligible: false,
    alert_request_needed: false,
    notification_key: null,
    message_preview: null,
    proposed_steps: [],
    diagnostics: [],
    delivered: false,
  };
}

function planItem(item, index, errors, reviews, now, routing, evidenceDiagnostics) {
  const decision = emptyDecision(item, index, errors);
  if (errors.length > 0) return decision;
  const author = authorIdentity(item.user);
  const diagnostics = [...evidenceDiagnostics];
  let affiliation = 'unverified';
  let source = 'unverified';
  if (author.bot) {
    affiliation = 'bot'; source = 'github_account_type';
  } else if (author.human) {
    const reviewed = reviewedAffiliation(author, reviews, now, diagnostics);
    if (reviewed !== null) {
      affiliation = reviewed;
      source = reviewed === 'unverified' ? 'review_requires_resolution' : 'reviewed_decision';
    }
  } else {
    diagnostics.push('author_unavailable');
  }
  const selectedLabel = `affiliation/${affiliation}`;
  const labels = item.labels ?? [];
  const remove = labels.filter(label => MANAGED_LABELS.includes(label) && label !== selectedLabel);
  const add = labels.includes(selectedLabel) ? [] : [selectedLabel];
  const labelsAfter = [...labels.filter(label => !remove.includes(label)), ...add];
  const eligible = affiliation === 'external' || affiliation === 'unverified';
  const requested = labels.includes(REQUEST_LABEL);
  const requestNeeded = eligible && !requested;
  if (requested) diagnostics.push('existing_request_requires_delivery_reconciliation');
  const key = JSON.stringify([REPOSITORY_ID, item.node_id, routing.channel_id, 'intake']);
  const steps = [];
  if (remove.length || add.length) steps.push({ kind: 'reconcile_affiliation_labels', remove, add });
  if (requestNeeded) steps.push({ kind: 'request_alert', label: REQUEST_LABEL, notification_key: key });
  let preview = null;
  if (eligible) {
    const kind = item.kind === 'pr' ? 'PR' : 'issue';
    const status = item.state === 'closed' ? 'already closed' : 'open';
    const draft = item.kind === 'pr' && item.draft ? '; draft' : '';
    const category = affiliation === 'external' ? 'reviewed external' : 'affiliation unverified';
    const url = `https://github.com/${REPOSITORY}/${item.kind === 'pr' ? 'pull' : 'issues'}/${item.number}`;
    preview = {
      channel_id: routing.channel_id,
      text: `${routing.mention_user_ids.map(id => `<@${id}>`).join(' ')} Please review ${kind} #${item.number} (${category}; ${status}${draft}).\nAuthor: ${author.human ? author.login : 'author unavailable'}\n<${url}|Open GitHub item>`,
      delivered: false,
    };
  }
  return { ...decision, affiliation, affiliation_source: source, label_changes: { remove, add }, labels_after: labelsAfter,
    alert_eligible: eligible, alert_request_needed: requestNeeded, notification_key: key, message_preview: preview,
    proposed_steps: steps, diagnostics: [...new Set(diagnostics)] };
}

/** Produce a synchronous, read-only plan from already obtained, sanitized evidence. */
export function planIntake(input) {
  if (!isRecord(input)) throw new TypeError('input must be an object');
  const routing = validateRouting(input.routing);
  const now = timestamp(input.evaluated_at);
  if (now === null) throw new TypeError('evaluated_at must be a valid RFC 3339 timestamp');
  if (!Array.isArray(input.items)) throw new TypeError('items must be an array');
  const reviews = Array.isArray(input.reviewed_decisions) ? input.reviewed_decisions : [];
  const evidenceDiagnostics = input.reviewed_decisions !== undefined && !Array.isArray(input.reviewed_decisions)
    ? ['reviewed_decisions_malformed'] : [];
  const errors = Array.from(input.items, itemErrors);
  markDuplicateItems(input.items, errors);
  const decisions = Array.from(input.items, (item, index) => planItem(item, index, errors[index], reviews, now, routing, evidenceDiagnostics));
  const counts = { input: decisions.length, classified: 0, invalid: 0, nvidia: 0, external: 0, unverified: 0, bot: 0, alert_eligible: 0, alert_request_needed: 0 };
  for (const decision of decisions) {
    if (decision.input_error.length) counts.invalid++;
    else { counts.classified++; counts[decision.affiliation]++; }
    if (decision.alert_eligible) counts.alert_eligible++;
    if (decision.alert_request_needed) counts.alert_request_needed++;
  }
  return {
    evaluated_at: input.evaluated_at,
    scope: { repository_id: REPOSITORY_ID, repository: REPOSITORY, channel_id: routing.channel_id },
    read_only: true,
    durable_deduplication: false,
    evidence_diagnostics: [...evidenceDiagnostics],
    counts,
    decisions,
  };
}

function runCli(args) {
  try {
    if (args.includes('--apply')) throw new Error('--apply is refused: this planner is read-only');
    if (args.length !== 2 || args[0] !== '--input' || !args[1] || (args[1] !== '-' && args[1].startsWith('-'))) throw new Error('usage: intake-plan.mjs --input PATH | --input -');
    const serialized = readFileSync(args[1] === '-' ? 0 : args[1], 'utf8');
    let input;
    try { input = JSON.parse(serialized); }
    catch { throw new Error('input is not valid JSON'); }
    const plan = planIntake(input);
    process.stdout.write(`${JSON.stringify(plan, null, 2)}\n`);
    process.exitCode = plan.counts.invalid ? 2 : 0;
  } catch {
    process.stderr.write('intake-plan: unable to produce plan; check input and configuration.\n');
    process.exitCode = 1;
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) runCli(process.argv.slice(2));
