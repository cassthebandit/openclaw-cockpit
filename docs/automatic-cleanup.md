# Automatic cleanup: specification and operator runbook

## Behavior

Finished managed assignments save a result and exit. The janitor archives and
removes their exited tmux session. A quiet terminal, expired hold or generic
runtime Stop does not establish completion.

Review holds last 24 hours by default and can be renewed. Releasing or expiring
a hold removes only that hold: it never stamps success or overrides newer work.
A hold without a valid deadline remains protected. Explicit keep-open is a
separate, deliberate indefinite choice. The supervisor rechecks saved completion,
new activity, ownership and the terminal before closing a retained assignment.

The launcher supports Claude reviewers with file tools but no shell access.
The per-assignment prompt explains how to save completion with Write. Matching
runtime hooks bind that file operation and the final reply to the assignment;
an arbitrary file, intermediate reply or another agent's event is insufficient.

Existing live sessions require explicit enrollment and a supported native runtime
profile. The janitor observes them for the configured quiet interval, saves their
output, requests native exit once and verifies removal. Profiles with pending
native proof cannot authorize automatic exit. Input, holds, attachments, service
protection and changed identity remain vetoes. Enrollment does not prove there is
no detached work: the owner must attest that separately.

## Session history

`<log_dir>/sessions.jsonl` records lifecycle events: starts/discovery, completion,
holds/releases, enrollment, exit requests, closure and failures or changed cleanup
reasons. Each record includes time, session, stable identity, action, source,
result and reason. Archive locations are included when relevant. It does not
contain prompts or transcripts.

The file defaults to **10,000,000 bytes**, configurable with `session_log_max_bytes` in lifecycle.json (integer 65536..2147483647). `log_max_bytes` still controls the separate service log. All writers must be restarted or finished before relying on a changed cap. New records use schema_version=1 with typed details; unversioned historical records remain v0. See [structured events](structured-events.md). Writers serialize access and trim the
oldest complete records. Saved results, captures and existing archive ledgers are
separate evidence, not deleted when history is trimmed. A failed required history
or archive write prevents the impending closure. An attempt is not confirmation.

## Rollout runbook

1. Validate the lifecycle configuration and record the current install revision,
   binary and service launch settings. Back them up before upgrading.
2. Run the repository checks and real disposable-session tests. Prove normal
   completion, shell-less completion and held completion followed by release.
   Test active work and changed identities survive. Run native profile checks for
   the exact supported runtime releases, not only mocks.
3. Install the reviewed revision. Restart only this tool's dashboard, janitor and
   inspector through the site's normal launchers. Confirm the resolved revision
   after actual dashboard polling and compare unrelated sessions before/after.
4. Keep existing-session selection explicit. Configure `live_retirement` as
   `observed`, `existing_session_mode` as `selected-only`, and exact names in
   `cleanup_whitelist`. Keep protected services in the blacklist/protected list.
   Enable `adopt_existing_exited` when exited-session adoption is wanted.
5. Preview with `python3 helpers/tmux/session_hygiene.py plan --json`. Inspect the
   selected sessions and their saved results. Deliberately retained or currently
   active work is not backlog. Release obsolete holds explicitly if needed.
6. Enroll each exact identity with `release-existing --prepare`. Supply the native
   profile, saved result, reason, wrapper path where required, and the required
   no-background-work attestation. `--dry-run` previews without mutation.
   Preparation saves current output, enables exit retention and stops the old
   pane pipe so the janitor's native-exit checks can work. A failed enrollment
   must be reported; never silently clear input or repeatedly rearm after activity.
7. Leave the janitor to perform observation, native exit and removal. Inspect
   `sessions.jsonl`, status and saved archives. Verify unrelated sessions survive.
   Manual closure does not count as automatic-cleanup acceptance. Remove temporary
   name selectors after a migration so future sessions reusing those names are
   not permanently selected.
8. On rollout failure, disable new enrollment/exit requests, restore the previous
   configuration/revision/binary and restart the affected tool services. Preserve
   evidence. Do not restart unrelated infrastructure to repair this tool.

Example enrollment (all values come from inspection, not a guessed identity):

```sh
python3 helpers/tmux/session_hygiene.py release-existing --prepare \
  --identity <identity-from-plan> --profile <supported-profile> \
  --result /absolute/saved-result.md --reason 'review finished' \
  --attest-no-background-work --dry-run
```

## Manual backup

The exact-session command saves output and closes a selected session. It does
not require native runtime version recognition and is **not** a janitor fallback.
It still protects services, linked/multipane sessions, attachments and identity.
A held session requires explicit `--override-hold` in addition to exact selection.

```sh
python3 helpers/tmux/session_hygiene.py kill-session --name <exact-session>
python3 helpers/tmux/session_hygiene.py kill-session --name <exact-session> \
  --identity <identity-from-preview> --reason 'approved closure' --execute
```

The first command only previews. The second archives, rereads identity and holds,
uses a synchronous tmux guard, verifies disappearance and reports any surviving
original processes. Saved output does not restore a running process or an unsaved
draft. Never interpret a missing success record as successful closure.

## Old assignment supervisors

A tool restart cannot replace a supervisor already running inside an older
session. The two `assignment-*` profiles support one pinned obsolete supervisor
implementation. Recovery requires the exact original `--assignment-dir`,
`--wrapper`, saved `--result`, and `--owner-reviewed-result`, in addition to the
normal exact identity, selection and background-work checks. When the accepted
report lacks a native completion receipt, `--allow-missing-receipt` is also
required; the original supervisor records **incomplete**, never invented success.
Other supervisor versions remain protected. The janitor holds the original
assignment lock across attempt persistence, final checks and native exit request, then lets
the original supervisor reap and classify its child before dead-pane cleanup.

Enrollment history is bounded to256 records. A temporary disappearance is not
permission to discard a consumed shutdown request. For an unused enrollment,
`revoke-existing --identity <exact-identity> --forget` explicitly revokes it and
frees its slot (`--dry-run` previews). Consumed exit attempts cannot be forgotten
this way. Confirmed automatic or manual closure removes that exact enrollment;
archives and action logs remain.

Automatic same-session Claude compaction is not new work and does not discard a
saved completion receipt. Submitted input, session replacement and new tool work
still invalidate it. A busy original-supervisor lock defers recovery without
consuming an exit attempt. An explicit tmux guard refusal records that no key was
sent and invalidates that release; an ambiguous send remains consumed and is
never automatically retried. Punctuation in goals or hold reasons is matched
literally, not rejected or evaluated as a tmux format.
