# Review retention and closeout

## Specification
Completed managed assignments must close their owned runtime after a requested review hold expires. The janitor removes only verified exited panes after archiving. Keep the existing assignment supervisor, hold evaluator, janitor and status file; no new service or dependency.

New `--keep-open` launches create a persisted review hold using `temporary_hold_hours` (24 hours by default). The deadline starts at launch and is not renewed by polling, redraw or restart. Explicit `keep-open --indefinite --hold-reason ...` remains the until-released pin. Old permanent keep-open bits and undated holds retain their meaning until an operator deliberately migrates the exact session.

The supervisor already retries retained completion after release/expiry. It must continue to check result integrity, current hook generation/session, owned child, pane identity, attachment/topology and composer before SIGTERM. No forced-kill timeout. Failed/incomplete assignments remain failed/incomplete. Genuine draft text is never submitted or cleared. Recognize only evidenced native empty-composer layouts; unknown layouts refuse closeout.

The planner must not advertise removal of live processes that its executor refuses. Completed live jobs report the owner's retention reason or waiting for runtime exit. Exited jobs retain existing archive and identity-revalidation behavior. Explicit enrollment is recovery only, not a prerequisite for new supervised jobs.

The footer distinguishes service freshness from cleanup condition. Count blocked jobs only from fresh identity-matched rows, excluding services/viewers and valid holds. Cards show hold deadlines, indefinite retention and specific blockers. Exit requests are not confirmed removals.

## Acceptance
- Timed launches expire without a permanent bit, explicit renewal works, indefinite/legacy holds survive.
- Existing supervisor tests prove resumed work invalidates completion, changed result/identity and attached/draft/unknown states prevent exit, and failed outcomes retain their classification.
- Empty native composer with the observed auto-mode footer is recognized; drafts and background work are rejected.
- Plan/apply agree on live retention; stale identity rows cannot inflate cleanup debt; status changes invalidate rendered cards/footer.
- `make check` passes, including disposable-socket integration tests.
- Disposable actual Claude and Codex jobs save a result, survive a review hold, exit after expiry and are archived/removed. Preserve active and pinned controls.

## Rollout and rollback runbook
1. Record source/build identities, current helper pin, service panes and unrelated pane PIDs. Back up binary and pin configuration. Keep private captures outside this public repository.
2. Complete checks and review correctness/design before merging. Verify PR and merged-main CI.
3. Install the exact clean reviewed revision using the existing installer and update the private integration pin through its normal PR. Relaunch only Cockpit dashboard/janitor/inspector as needed. Never restart Gateway.
4. Verify clean helper/build identity after real dashboard polling, service health, actual disposable closeout and preservation of unrelated panes.
5. Handle the old backlog separately: inspect exact pane/process identities, completion records, visible drafts and activity; preserve results/transcripts/continuation details. Do not hot-reload old supervisors, fabricate successful outcomes, or weaken recovery checks. Retire only individually confirmed finished/abandoned work; keep active and explicitly pinned work.
6. Verify original processes and panes are gone and archives exist. Record any retained item and its reason.
7. On rollout failure restore the backed-up coherent binary/pin pair and relaunch only Cockpit services. Verify original unrelated pane identities and restored health. No Gateway restart. Do not retry an unsafe ownership/source-of-truth architecture without reassessment.
