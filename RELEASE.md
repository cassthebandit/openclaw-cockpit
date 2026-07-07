# Release Guide

This project ships from an annotated Git tag through GitHub Actions and GoReleaser. Homebrew tap automation should be added only after the new OpenClaw Cockpit repo has an owned tap target.

## 1. Prep the repository
1. Update `cmd/openclaw-cockpit/main.go` with the new semantic version.
2. Update package metadata such as `flake.nix` and version examples in `README.md`.
3. Move unreleased notes into `CHANGELOG.md` under the new version heading.
4. Run formatting, `go test ./...`, `golangci-lint run`, cross-platform builds, and a GoReleaser snapshot.
5. Live-test the snapshot binary against a real tmux session, then commit and push the release prep.
6. Require green `main` CI, a clean checkout, and an empty GitHub issue/PR queue.

## 2. Tag and publish on GitHub
1. Create an annotated tag: `git tag -a vX.Y.Z -m "Release vX.Y.Z"`.
2. Push the tag: `git push origin vX.Y.Z`.
3. Watch the `release` workflow. GoReleaser publishes macOS, Linux, and Windows archives plus `checksums.txt`.
4. Set the GitHub Release notes from the matching changelog entry and verify the new release is marked latest.

## 3. Update package channels
1. Verify GoReleaser published the expected OpenClaw Cockpit archives and checksums.
2. If an owned Homebrew tap exists, update the formula there and test it locally.
3. Do not dispatch or update Peter Steinberger's `steipete/homebrew-tap` from this repo.

## 4. Post-release checks
- Verify the tag, GitHub Release, assets, checksums, release notes, release workflow, and Homebrew workflow.
- Restore an empty `Unreleased` changelog section for the next patch, commit, and push.
- Confirm `main` CI is green and the final checkout is clean.
