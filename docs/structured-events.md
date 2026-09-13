# Structured session events

Session history is JSONL at `<log_dir>/sessions.jsonl`. Schema: [session-event.schema.json](session-event.schema.json). All new events include schema_version=1, event_id UUID, component, reason_code and typed details. Existing time/session/identity/event/source/result/reason fields remain compatible. Consumers must never parse reason prose or treat unknown codes as success.

Components: assignment, janitor, holds, adoption, manual_close, logger. Lifecycle events include start, completion, retention, exit_attempt, exit, hold, hold_release, hold_end, keep_open_release, discovered, cleanup_state, adopted, enrollment_prepared, adoption_revoked, adoption_forgotten, kill_attempt, kill_result and manual_close_attempt/result. Producers retain their existing codes. Native process completion and session removal are separate events.

`details.action_outcome` normalizes attempted/succeeded/failed/unknown; top-level result keeps historical spelling. action_id joins an operation's attempt/result, identity joins one session incarnation. Assignment events carry run/runtime/model/working-directory and result references where available. Holds carry typed expiry; no expiry does not establish completion. Unknown metadata is null, never inferred. `reason_code` is the stable reason family before dynamic colon-delimited details, or unspecified for legacy free text. It is not an error-message parser or an instruction.

The cap defaults to 10,000,000 bytes; `session_log_max_bytes` is configurable separately from service log_max_bytes. The 8192-byte record bound remains. Invalid required IDs/paths are rejected, never silently truncated. Details use an explicit allowlist, not arbitrary runtime payloads. No prompts/transcripts/credentials are included.

Concurrent writes use the existing stable lock and fsync/atomic whole-line trim. Trim inserts history_trimmed with details.history_incomplete=true, retained across subsequent trims. Absence of the marker means unknown coverage, not complete history. Archived results are separate and untouched. Pre-action logging failure blocks the action; post-action failure is uncertainty, not permission to retry.

Read-only examples:

```sh
jq -c --arg id EXACT_IDENTITY 'select(.identity == $id)' sessions.jsonl
jq -c 'select(.details.action_outcome == "failed" or .details.action_outcome == "unknown")' sessions.jsonl
jq -c 'select(.event == "history_trimmed")' sessions.jsonl
```

An unterminated historical tail blocks subsequent writes under the lock; preserve/inspect the damaged file before recovery. Writers never fabricate a repaired historical record. Malformed JSON fails the query; do not call a partial result complete. Query current lifecycle status before treating historical intervention flags as current. Unknown versions remain unknown.

For bounded results plus full-file coverage reporting, use `python3 -B helpers/tmux/event_log.py /absolute/sessions.jsonl --identity EXACT_ID --limit 1000`. Output includes history_complete (false/null), legacy/unknown-version counts, invalid line numbers, query_truncated and events. Invalid JSON returns exit 1; it does not silently disappear. Output keeps only the newest matching limit records. The reader reports parse/version coverage, not semantic certification of arbitrary historical data.

## Producer catalogue (v1)

Legacy top-level results remain as listed below. Use typed action_outcome for operations, assignment_state for work outcomes, and process_state for process facts. Observation events never carry action_id/action_outcome from earlier operations.

- Assignment `start` / `starting`: run_id, runtime, model and working_directory (nullable if unavailable).
- Assignment `completion`: assignment_state and completion_receipt_path/result_path from the accepted receipt. `retention` and `hold_end` describe lifecycle protection, not process success. `exit_attempt` / `requested` and `exit` / `exited` or `unconfirmed`: action=process_exit, correlated action_id where an exit was requested, process_state and known exit_code. An unrequested exit has null action_id. An incomplete assignment may have a successfully observed process exit.
- Holds `hold` / `attempt,set`; `hold_release` and `keep_open_release` / `attempt,released`: action=hold_change, one action_id per invocation, attempted/succeeded, expires_at and indefinite where supplied. Actor/hold identifiers are absent when the owner does not provide them.
- Janitor `discovered` / `observed` and `cleanup_state` / the owner's state: typed decision retains owner codes such as active, protected, operator_prompt, marked_for_teardown, cleanup_pending, cleanup_blocked, exit_requested, shutdown_unconfirmed, removed (or fallback action skip/refuse/mark/kill/request_exit). These are observations, not action results or scheduled promises. Known skip/refusal reason families are blocking_conditions. requires_intervention remains null because the owner does not expose that independent fact.
- `kill_attempt` / `attempt`, `kill_result` / `success,failed`: action=session_close, correlated attempted/succeeded/failed/unknown. `closure_verification_unavailable` and `closure_unconfirmed` have action_outcome=unknown even though legacy result=failed. Error codes reflect owner verification, not an assumed safe retry.
- `manual_close_attempt` / `attempt`, `manual_close_result` / `success,failed`: same session_close contract, component manual_close. surviving_processes is the actual remaining process list when checked; absence is unknown, an empty list is a checked empty result.
- Adoption `exit_request_prepared` / `attempt`, `exit_request_result` / `success,failed`: action=native_exit_request, one correlated action_id. Success establishes request delivery only, NOT process exit. Nonzero ambiguous send is unknown with error_code=native_exit_request_unconfirmed; tmux_guard_changed is a known refusal/failed attempt. Component adoption.
- Adoption `enrollment_prepare_attempt` / `attempt` and `enrollment_prepared` / `saved output; native exit retention ready`: action=enrollment_prepare with shared attempt/result ID; archive_path when saved. `adopted` / `registered` is the existing pre-save observation, not new verification of persisted state. `adoption_revoked`/`adoption_forgotten` / `attempt` is attempt-only action=adoption_revoke with unique action_id; it does not claim confirmed state write.
- Logger `history_trimmed` / `trimmed`, reason_code=size_limit, history_incomplete=true: non-session retention marker.

Reason-code families preserve the existing owner identifier before `:`. Stable examples: managed_kill_on_done (eligible managed cleanup), native_exit_requested (request sent), owner_revoked (adoption invalidation), indefinite/expires (hold policy), managed_assignment_exit/retained_assignment_exited (observed exit), process_exited_without_assignment_completion (no accepted completion), archive_failed/ledger_failed/metadata_write_failed/exit_request_failed (owner failure). Free-text-only reasons use unspecified; unknown/additive codes must not imply success. Dynamic diagnostic suffixes remain legacy reason text and are never required for scripting.

Known failure observations expose error_code as that owner failure family, operation=cleanup_observation, retryable=null. Action result errors use their concrete owner code above, or action_failed when only nonzero failure is established; operation names the action. This catalogue does not claim every legacy prose error has a finer machine classification. The writer validates allowlisted types and the schema's four action-outcome values; producer fixtures in helpers/tests/test_event_log.py exercise actual adapters, not only hand-authored JSON.

To find incomplete/failed assignments, query that separate fact as well:

```sh
jq -c 'select(.details.assignment_state == "incomplete" or .details.assignment_state == "failed" or .details.action_outcome == "failed" or .details.action_outcome == "unknown" or .details.error_code != null)' sessions.jsonl
```

Run the full-file coverage reader described above before interpreting this selection. A successful process_exit does not override an incomplete assignment.
