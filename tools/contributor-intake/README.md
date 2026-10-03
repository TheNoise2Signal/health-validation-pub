# Contribution intake

The workflow labels new issues and PRs, including drafts, and alerts maintainers to
reviewed external authors and authors with unverified affiliation. It starts
disabled and is intended for a supervised pilot.

## Flow and shared queue

On an opening, reviewed code from `main` fetches current GitHub metadata and
maintains one `affiliation/*` label. NVIDIA authors and GitHub Bot accounts receive
no targeted alert. External and unverified authors get a `triage/alert-requested`
marker before one Slack send attempt. The process validates the actual response
without logging private routing or receipts; the operator records the post's
permalink in a private journal.

Alerts contain configured recipient mentions, the item number, open/closed state,
draft status, affiliation, author login and GitHub link. Contribution titles,
bodies, comments and private review evidence are omitted.

Shared views:

- [Open external items](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aopen%20label%3Aaffiliation%2Fexternal)
- [Open unverified items](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aopen%20label%3Aaffiliation%2Funverified)
- [Open NVIDIA items](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aopen%20label%3Aaffiliation%2Fnvidia)
- [Items without an affiliation label](https://github.com/dsx-ai-factory/health-validation/issues?q=is%3Aopen%20-label%3Aaffiliation%2Fnvidia%20-label%3Aaffiliation%2Fexternal%20-label%3Aaffiliation%2Funverified%20-label%3Aaffiliation%2Fbot)
- [All requested alerts](https://github.com/dsx-ai-factory/health-validation/issues?q=label%3Atriage%2Falert-requested)

The workflow adds the request label before posting. Check delivery and unresolved
requests in the private journal.

## Affiliation evidence

Only reviewed evidence tied to the author's numeric GitHub account ID determines
NVIDIA or external status. Access roles, CODEOWNERS, team membership,
`author_association`, labels, profile company and commit email cannot establish
employment. Missing evidence remains unverified. Bot status comes from the item
author's GitHub account type.

Keep review history in private storage, outside source, logs and workflow
artifacts. Supply one current record per author through the
`HEALTH_VALIDATION_REVIEWED_DECISIONS_JSON` Actions secret. Every
field below is required. The example uses synthetic values; reviewers must choose
actual expiry dates:

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

Missing fields, conflicting current records, mismatched IDs, future reviews and
expired evidence leave the author unverified. A numeric ID survives a login
rename; a reused login cannot transfer the previous account's affiliation.
Missing, malformed or non-array registry JSON also leaves authors unverified and
emits a generic diagnostic. An empty registry classifies every human as unverified.

Affiliation is reviewed manually; the workflow has no corporate directory
integration.

## Provision and activate

Before enabling the workflow:

1. Review and merge the disabled workflow and scripts into `main`.
2. Verify label definitions: `affiliation/nvidia`, `affiliation/external`,
   `affiliation/unverified`, `affiliation/bot` and `triage/alert-requested`.
3. Provision an approved bot token as `HEALTH_VALIDATION_SLACK_BOT_TOKEN` through
   Actions secret management. Verify `chat:write`, pilot-channel access and that
   intended recipients receive its mentions.
4. Provision the reviewed registry secret above and review existing authors.
5. Provision `HEALTH_VALIDATION_INTAKE_ROUTING_JSON` as one Actions secret with
   `channel_id` and `mention_user_ids`, for example
   `{"channel_id":"C0123456789","mention_user_ids":["U0123456789","U0987654321"]}`.
   Keep the whole object in a secret to mask runner logging before code starts.
   Verify runner/pinned-action policies, GitHub/Slack egress and credential access
   for actual fork PR events.
6. Name an operator for affiliation review, queue checks and delivery
   reconciliation. Maintain a shared private journal with repository/item identity,
   run reference, decision revision, delivery state, Slack permalink, check time
   and reviewer.
7. Set `HEALTH_VALIDATION_INTAKE_ENABLED` to exactly `true` as a repository variable
   for an approved supervised window. Other or absent values keep the job off.

The workflow does not provision labels, apps, channel access or secrets. Its
native HTTPS POST to Slack has an absolute 10-second deadline covering connection
and response consumption, a 64 KiB response limit, no redirects and no automatic
retries. Routing and raw receipts stay inside the trusted process, without
workflow outputs or separate step inputs. PR code is never executed.

## Supervised checks and limits

Record actual results for:

- Issue, ordinary PR and draft PR from an unverified author.
- Reviewed external, reviewed NVIDIA and actual GitHub Bot authors.
- Correct destination, recipient mentions and elapsed time from opening to post.
- Preservation of unrelated labels and assignees.
- Replay with an existing intent: reconciliation is required and no post is made.
- Missing sender, invalid routing, GitHub failure, Slack rejection and ambiguous
  timeout or interruption.

A failed or interrupted run may already have posted. Inspect Slack and the private
journal before an authorized retry; do not blindly rerun or delete intent. Record
the post's permalink privately even after a successful run. Disable verbose
debugging with private inputs.

At agreed review times, compare queues with the journal and workflow runs. Include
closed items when checking missed openings or uncertain sends. Opening events do
not backfill old items or refresh changed/expired affiliations; the operator must
review these cases. Per-item concurrency and intent labels do not guarantee
exactly-once delivery: pending jobs can be cancelled, labels can change and events
can be missed. This pilot requires human reconciliation; unattended operation is
outside the scope of this workflow.

## Verification

Tests use the repository's existing Bazel Node toolchain, with no npm dependencies:

```sh
bazel test //tools/contributor-intake:all
bazel test //...
bazel run //tools/format
```

For quick iteration with Node 22 or newer:

```sh
node --test tools/contributor-intake/*.test.mjs
```
