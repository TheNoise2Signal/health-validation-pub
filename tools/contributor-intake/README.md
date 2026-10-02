# Contribution intake

This supervised pilot gives maintainers a shared issue/PR queue and opening
notifications for reviewed external authors and authors whose affiliation is
unverified. It includes draft PRs. The workflow is disabled by default.

## Flow

1. An issue or PR opens in `dsx-ai-factory/health-validation`.
2. Actions executes reviewed code from `main` and fetches current GitHub metadata.
3. A private reviewed registry determines the author's affiliation using their
   numeric GitHub account ID. The script maintains one `affiliation/*` label.
4. NVIDIA authors and GitHub Bot accounts receive no targeted notification.
   External and unverified authors get a `triage/alert-requested` marker followed
   by one Slack send attempt to the configured channel and recipients.
5. The script checks Slack's actual success, channel and message timestamp. An
   operator records the receipt privately and reconciles failures before retrying.

PR code, branches, titles, bodies and comments are never executed. Alert text
contains the item number, lifecycle, affiliation category, validated author login
and canonical GitHub link. It omits contribution text and private review evidence.

## Shared queue

These views include issues and PRs, including drafts:

- [Open external items](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aopen%20label%3Aaffiliation%2Fexternal)
- [Open unverified items](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aopen%20label%3Aaffiliation%2Funverified)
- [Open NVIDIA items](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aopen%20label%3Aaffiliation%2Fnvidia)
- [Items without an affiliation label](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aopen%20-label%3Aaffiliation%2Fnvidia%20-label%3Aaffiliation%2Fexternal%20-label%3Aaffiliation%2Funverified%20-label%3Aaffiliation%2Fbot)
- [All requested alerts](https://github.com/dsx-ai-factory/health-validation/issues?q=label%3Atriage%2Falert-requested)

A request marker does **not** prove delivery or indicate an unresolved alert. Use
the private receipt journal to distinguish successful and unresolved requests.
Opening events do not backfill old items or revisit changed affiliation records.
The pilot operator must review those cases explicitly.

## Affiliation evidence

Repository access, CODEOWNERS, team membership, `author_association`, existing
labels, profile company and commit email are not employment evidence. Missing
evidence never establishes external status. Only GitHub's account type determines
the bot category; the event sender does not determine the item author's category.

Keep authoritative review history in approved private storage. Supply a JSON
array through the `HEALTH_VALIDATION_REVIEWED_DECISIONS_JSON` Actions secret. Each
record requires these fields; the example is synthetic:

```json
[
  {
    "author_id": 123456,
    "login": "example-contributor",
    "affiliation": "external",
    "reviewed_by": "authorized-reviewer",
    "evidence_ref": "private-record-reference",
    "reviewed_at": "2026-10-01T12:00:00Z",
    "expires_at": "2026-10-08T12:00:00Z",
    "revision": "review-1"
  }
]
```

The example dates are not an affiliation policy. Reviewers choose and record
actual expiry dates. Supply one current record per author. Records with missing
fields, conflicting current revisions, mismatched numeric IDs or expired evidence
cannot suppress an alert. A stable numeric ID can survive a login rename; a reused
login cannot transfer a previous account's affiliation.

Missing, malformed or non-array registry JSON leaves authors unverified and emits
a generic diagnostic without printing the registry. Do not seed the registry from
current labels. Never commit private records or upload them as workflow artifacts.
This pilot has no corporate directory integration.

## Provision and activate

An authorized operator must complete these steps before enabling the workflow:

1. Review and merge the scripts and workflow into `main`.
2. Verify these label definitions exist: `affiliation/nvidia`,
   `affiliation/external`, `affiliation/unverified`, `affiliation/bot`, and
   `triage/alert-requested`.
3. Provision an approved Slack bot credential as the Actions secret
   `HEALTH_VALIDATION_SLACK_BOT_TOKEN`. Verify permission to post to the pilot
   channel and that intended recipients can receive its mentions. Credentials
   must go through secret management, never chat or source.
4. Provision the reviewed registry secret above. An empty registry preserves
   visibility, but labels every human as unverified; review the existing authors
   before judging alert volume or classification accuracy.
5. Set repository variables `HEALTH_VALIDATION_INTAKE_CHANNEL` to the intended
   channel ID and `HEALTH_VALIDATION_INTAKE_MENTION_USER_IDS_JSON` to a JSON array
   of recipient IDs, such as `["U0123456789", "U0987654321"]`. Use real operator
   settings, not these synthetic examples. Confirm Actions policies allow the
   pinned checkout, setup-node and Slack actions and the `ubuntu-latest` runner.
6. Name the operator for affiliation review, queue checks and delivery
   reconciliation. Keep a shared private journal with repository/item identity,
   run reference, decision revision, delivery state, actual Slack receipt,
   checked-at timestamp and reviewer.
7. Enable the repository variable `HEALTH_VALIDATION_INTAKE_ENABLED` with exactly
   `true` for a supervised test window. Absent or other values keep the job off.

GitHub receives only the affiliation labels and request marker. The workflow does
not provision label definitions, bot installations, channel access or secrets.
Check its execution policies and credential access for actual fork PR events.

## Supervised verification

Create approved controlled openings and record actual results for:

- Issue, ordinary PR and draft PR from an unverified author.
- A reviewed external author, reviewed NVIDIA author and actual GitHub Bot author.
- Correct recipient mentions, destination and source-to-receipt time.
- Preservation of unrelated labels and assignees.
- Replay with an existing request marker: it stops for reconciliation and never
  produces a second send payload.
- Missing sender, invalid routing, GitHub failure, Slack rejection and ambiguous
  timeout/interruption. Do not delete an intent or rerun blindly.

If delivery is uncertain, check Slack and the private journal before an authorized
retry. The Slack action has automatic retries disabled. A failed or interrupted
run may still have posted a message. A green run and an intent label do not replace
the private receipt record. Keep evidence and receipts out of public logs and
artifacts; do not enable verbose debugging with private inputs.

At agreed review times, compare the GitHub queue with the journal and workflow
runs. Include closed items when checking for missed openings or uncertain sends.
Correct changed/expired affiliation records and labels through supervised review.
An existing all-activity feed may remain a separate pilot safety net; measure its
noise before choosing production routing.

## Verification commands

The package has no npm dependencies. Its tests are included in the repository's
Bazel test suite using the existing Node toolchain:

```sh
bazel test //tools/contributor-intake:all
bazel test //...
bazel run //tools/format
```

For quick local iteration with Node 22 or newer:

```sh
node --test tools/contributor-intake/*.test.mjs
```

`intake-plan.mjs --input PATH` (or `--input -`) generates a read-only preview from
sanitized item metadata, reviewed records and explicit routing. It refuses apply
mode. Run previews privately when the input contains affiliation records.

## Rollout boundary

This pilot is not an unattended notification service. Per-item concurrency and a
GitHub label do not provide durable delivery accounting or exactly-once sends.
GitHub can cancel pending concurrency jobs, operators can alter labels, and events
or workflow runs can be missed. Human queue and receipt checks are required here.

Before unattended rollout, add private durable receipts, bounded dispatch,
periodic paginated recovery across open and closed items, affiliation refresh and
visible failures/missing runs. Use pilot evidence to choose production routing
and acceptable alert volume. Keep the activation variable false until those
operational responsibilities and the supervised live checks are confirmed.
