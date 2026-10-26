---
name: ticket-release
description: Get this Arandu package green, declared and published. Use when the request is to "run the gates", "check it builds", "the tests fail", "go build says the directory prefix does not contain modules listed in go.work", "aru doctor says this is not an Arandu project", "update arandu.mod.toml", "add a dependency", "bump the framework", "tag a release", "publish it", "write the changelog", "write the upgrade guide", or before opening a pull request. Covers the gates and why each filter is there, what the manifest declares and who checks it, the immutability of a published module version, and what breaks somebody else's build.
license: MIT
---

# Getting it green and out

## The gates

Nothing is finished until all of these exit zero.

```sh
export GOWORK=off
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go')
go build ./...
go vet ./...
go test -race -p 2 ./...
aru model:build --check
```

`GOWORK=off` first. This checkout may sit beside a Go workspace that lists the
framework repositories and does not list this one; when it does, `go build ./...`
exits 1 before compiling anything with *"directory prefix . does not contain
modules listed in go.work"*. With the workspace off, the module resolves the
versions in `go.mod`, which is what CI compiles against and what somebody's
`go get` gets.

`-p 2` because the machine is shared: one suite at a time.

`aru doctor` is not a gate here. It reads applications and exits 1 on a library
(*"this is not an Arandu project"*). What it would check of an installed package
is held by `tests/Unit/audit_test.go`, and the doctor is run over an application
that wires the package.

`aru model:build --check` writes nothing and exits 1 when a generated query is
missing, stale or edited by hand. It needs the `aru` binary.

`TestEveryPublishedViewCompiles` publishes the views into a scratch project,
compiles them with the view compiler pinned in the test (`aru@v0.74.0`), builds
them and draws every screen once. Move that version to take on a stricter
compiler, and fix the view it refuses here rather than in every application.

## What the manifest declares, and who reads it

`arandu.mod.toml` is a promise, and the check that gives it weight runs here.
`aru doctor` audits the tree of the application it is run inside and never loads a
dependency, so the manifest of an installed package is opened by nothing but that
package's own suite.

```toml
name = "hyz-is/ticket"
framework = ">= 0.66"
profiles = ["conventional", "performance"]

[permissions]
network = false
filesystem = false
exec = false
migrations = true
```

`TestTheDeclaredCapabilitiesAreWhatTheCodeDoes` compares the four permissions
against what the code *calls*, by AST. Two findings come out of it:
`permission-not-declared` (the code does it and the manifest says `false`) and
`permission-not-used` (the manifest says `true` and nothing does it). Both fail
the suite. Attachments go through the application's Disk, not through `os`, which
is why `filesystem` is `false`; an `os.WriteFile` added here changes the manifest
in the same commit.

The `framework` line is a floor and a second declaration of what `go.mod` says:
`framework = ">= 0.66"`. Move both together when you bump.

## Dependencies

This module goes into other people's builds, so a `require` is a download and an
audit surface for every project that installs it. The direct ones are the
framework, hesape, kyse (the screens), arandu-activitylog and arandu-markdown, all
of which the ecosystem rules ask for; the SQLite connector is for the tests. A new
one comes with a note saying what it does that the framework does not.

## Publishing

A published Go module version is **immutable**: the proxy serves it forever, and a
mistake is corrected by another release, never by moving a tag.

Before tagging:

- the gates pass with `GOWORK=off`;
- `arandu.mod.toml` matches the code;
- `CHANGELOG.md` has the entry under the version, in the Keep a Changelog
  sections (`Added`, `Changed`, `Fixed`, `Removed`), naming every action and every
  migration (`TestEveryActionIsNamedInAReleasedChangelogEntry`,
  `TestEveryMigrationIsNamedInAReleasedChangelogEntry`);
- `UPGRADE.md` has the matching `## vX.Y.Z` heading.

While the version starts with `v0.`, breaking is allowed. Breaking *quietly* is
not. Run `apidiff` against the release baseline and enumerate every removal; the
CI step `api diff against the last release` fails unless `UPGRADE.md` names each
one. Three changes break an installer without touching a signature, and each is
worth a line in the changelog: **a route name** (URLs are built from names), **a
migration name**, and **a default** (`DefaultPrefix`, `DefaultTeamPrefix`,
`DefaultPageSize`, `DefaultLocale`, `DefaultContactWindow`).

## Reporting a vulnerability

Not in an issue and not in a pull request. `SECURITY.md` has the private advisory
address. Anything that lets a caller reach data a policy did not authorize is in
scope: a handler that reaches a Model directly, a route outside `RequireAuth`, a
Model terminal before `Authorize`, a Grant built outside `grants.go`, a customer
response that carries an internal note or the team's triage, a file served
without the policy.
