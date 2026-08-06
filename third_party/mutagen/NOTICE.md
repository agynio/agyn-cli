# Vendored Mutagen

A subset of [Mutagen](https://github.com/mutagen-io/mutagen) `v0.18.1`, vendored
rather than taken as a module dependency.

## What this is

Reconciliation, the entry and snapshot model, conflict detection, ignore
handling, symbolic-link modes, and permission mapping, from
`pkg/synchronization/core` and what it transitively needs. See
[Sandbox Workspace Sync — Sync Engine](https://github.com/agynio/architecture/blob/main/architecture/sandbox-sync.md#sync-engine).

Mutagen's own daemon, session manager, and agent-pushing machinery are **not**
here. Those are what would put a second binary on each end and a version
negotiation protocol between them; the endpoint in this design is a subcommand
of a CLI that is already present in every workload.

## Why vendored rather than required

The Mutagen module is mixed-licensed. Everything here is under the MIT license
in `LICENSE`. Nothing under the upstream `sspl/` tree — fanotify watching, zstd
compression, xxh128 hashing — is vendored, and nothing here reaches it: that
code is additionally gated behind the `mutagensspl` build tag, so a plain build
cannot reach it even by accident.

Because the module as a whole is mixed-licensed, a live dependency would be
flagged by license scanning and inventory. Vendoring the MIT packages keeps the
dependency graph honest and makes importing the wrong package structurally
impossible rather than merely discouraged.

## Deviations from upstream

Two, both deliberate:

- **Import paths rewritten** from `github.com/mutagen-io/mutagen/...` to
  `github.com/agynio/agyn-cli/third_party/mutagen/...`. Nothing else in the
  source is edited, so a diff against upstream stays readable.
- **Windows-only files removed** (`*_windows.go` and
  `pkg/filesystem/internal/third_party/os`). The CLI ships darwin and linux
  only. They are dropped rather than kept because they require
  `github.com/hectane/go-acl`, which requires `golang.org/x/sys v0.46.0`, which
  requires Go 1.25 — and the build images pin Go 1.24. `GOOS=windows` therefore
  does not build; restoring those files and bumping the toolchain is what a
  Windows target would need.

Test files are not vendored.

`go vet` and `go test` in CI are scoped to `./cmd/...` and `./internal/...` for
the same reason: vetting reports style in code we do not own, and changing it to
satisfy the linter would break the diff against upstream that makes a refresh
reviewable.

## Third-party code within the subset

The vendored packages carry no forked code under a foreign license. The one
file that did — `pkg/filesystem/internal/third_party/os/path_windows.go`,
adapted from the Go standard library under the Go Authors BSD license — is
Windows-only and is among the files removed above.

## Refreshing

1. `go get github.com/mutagen-io/mutagen@<version>` in a scratch module.
2. Copy the package list in `refresh.txt` from the module cache.
3. Delete `*_test.go` and the Windows-only files named above.
4. Rewrite the import path prefix.
5. Confirm nothing under `sspl/` entered the tree:
   `grep -rl "mutagen/sspl" third_party/` must be empty.
