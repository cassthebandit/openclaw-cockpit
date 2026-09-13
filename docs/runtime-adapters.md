# Contributing a runtime adapter

Cockpit welcomes pull requests for AGY, Kilo Code, Grok Code and other runtime
interfaces. A runtime adapter recognizes a **specific tested interface**, not a
model brand. CLI, IDE, API and operating-system variants may require different
profiles. Adding a name is not evidence that live retirement works.

## One module and one registration

1. Add `helpers/tmux/runtime_adapters/<runtime>.py` following the built-ins.
2. Import it and append it to `BUILTINS` in that package's `__init__.py`.
3. Add recorded, sanitized screens, process-tree tests and native integration
   evidence. Run the common tests and `make check`.

No changes to the cleanup planner, policy or terminal writer should be necessary.
There is no automatic module discovery, arbitrary plugin path, install hook,
network download or user-configurable exit command. Adapters are reviewed Python
code shipped with Cockpit, not sandboxed third-party plugins.

## Module contract

- `ADAPTER_ID`: unique lowercase identifier.
- `PROFILES`: unique profile IDs mapped to dictionaries containing `adapter`,
  `platform` (Python platform name), `command` (observed tmux receiver), `version`,
  `capture_ansi` (boolean), `keys` (tuple of tested native tmux keys), and `proof`
  (description/location of the evidence). Optional `redraws_while_idle=True`
  requires explicit owner sampled-screen consent. Keep existing IDs stable.
- `runtime(pane, profile, record, *, process_path, legacy_wrapper)` returns
  `(identity_dict, "")`, or `({}, refusal_reason)`. Read-only. Identify the actual
  receiver, executable/build and process birth; account for every descendant.
  Bind relevant helper/configuration identity in the returned dictionary so
  changes reset the quiet observation. Unavailable evidence must refuse.
- `prompt_reason(hygiene, profile, raw_capture)` returns an empty string only for
  the tested completed, empty-composer state; otherwise a diagnostic reason.
  Distinguish placeholders from drafts, multiline input, active work, approval,
  interruption and background work. ANSI can carry essential evidence.
- Optional `enrollment(args, read_bytes)` returns runtime-specific evidence to
  save with the release (currently the historical Claude wrapper). It must not
  overwrite common release fields or perform effects. Existing CLI options are
  available; introduce another option only when an interface truly requires it.

The `legacy_wrapper` callback is retained for the historical Claude launcher;
other adapters ignore it. Adapters must not send keys/signals, close sessions,
write lifecycle state, select targets, or weaken protection rules. Callback code
is trusted reviewed code; the interface is an ownership boundary, not a sandbox.

## Required proof for live support

Include a disposable isolated-server run showing enrollment, quiet observation,
archive-before-exit, the native exit gesture, actual dead status, exact removal,
no resend on repeat, saved result/final output, and an untouched sibling sentinel.
Record the runtime version, OS/architecture, installation form and exit settings.
Include negative cases for typed/multiline input, active children, approval,
interruption, changed executable/configuration and unsupported versions. Exercise
common hold/blacklist/attachment protections; do not replace them in the adapter.

A fake adapter in the common suite demonstrates registration and dispatch without
new cleanup branches. All registered profiles run common protection/platform tests.
Unit fixtures alone do not establish support for a new native interface.

## Current support

- Claude 2.1.270 on macOS: direct executable and the exact historical Bash wrapper.
- Codex 0.153.4 on macOS: tested npm/native installation and enumerated helpers.
- AGY 1.2.2 on macOS arm64: tested standalone binary content, same-user HOME and
  an explicit unambiguous Ctrl-D exit binding. Missing/custom conflicting bindings,
  other builds and unexplained children refuse. AGY may rename a running binary
  during automatic updates; the tested content hash must still match.

These are **observed-mode** profiles with explicit owner enrollment and background
work attestation, not telemetry-backed proof of completion. Runtime-neutral policy
is shared, but safe native shutdown cannot be assumed universal. Unsupported
interfaces remain visible/protected; exited-session adoption is a separate path.
GUI-only interfaces may need a new supported control surface; do not impersonate
a tmux receiver or invent a generic kill fallback to fit this contract.

AGY native gesture/keybinding reference:
[Antigravity CLI usage](https://antigravity.google/docs/cli/using).

### AGY redraws and sampled-screen consent

AGY 1.2.2 continuously redraws an unchanged terminal; tmux's output timestamp
advances even when no visible work changes. AGY enrollment therefore additionally
requires `--attest-screen-sampling`. This explicit per-release choice uses exact
captured-screen hashes plus process/build/config/result identity for quietness,
not the changing output timestamp. The common engine owns this choice; an adapter
cannot silently activate it. Existing releases remain timestamp-strict by default.

This accepts that activity which appears and disappears between observations can
be missed. It is not telemetry-backed completion. Drafts, visible pending work,
changed captures/processes/results, holds, attachments and other common guards
still veto. The attestation is saved with the release and archive. Without this
choice AGY enrollment refuses with `screen_sampling_attestation_required`.

Example (exact disposable/owner-approved target only):

```sh
python3 helpers/tmux/session_hygiene.py release-existing \
  --socket-path /absolute/path/tmux.sock --lifecycle-config /absolute/path/lifecycle.json \
  --identity ID_FROM_PLAN --profile agy-1.2.2-direct \
  --result /absolute/path/result.md --reason 'Temporary assignment finished' \
  --attest-no-background-work --attest-screen-sampling --dry-run
```
