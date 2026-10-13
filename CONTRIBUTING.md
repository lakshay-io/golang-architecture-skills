# Contributing

## Layout

```
.claude-plugin/marketplace.json          marketplace catalog (lists the plugins)
plugins/golang-architecture/
  .claude-plugin/plugin.json             plugin manifest: name, version
  skills/go-backend-scaffold/
    SKILL.md                             what Claude reads: when to trigger and how to work
    scripts/scaffold.py                  copies the template and renames the module and service
    assets/template/                     the Go service template: a real, buildable module
  evals/                                 eval cases for `claude plugin eval`
.github/workflows/ci.yml                 checks every PR must pass
```

The template is a working Go module named `example.com/goservice`, with service name `goservice`. `scaffold.py` replaces both when it generates a service.

## Changing the template

```sh
cd plugins/golang-architecture/skills/go-backend-scaffold/assets/template
make verify          # gofmt, vet, module integrity, race tests, build
golangci-lint run    # v2; CI runs the same version
```

Keep the template **architecture-only**: no business features. Any rule you change belongs in its `docs/ARCHITECTURE.md` too, with the reason. The skill and the team both rely on that document.

## Trying the plugin locally

```sh
claude --plugin-dir plugins/golang-architecture                  # session with your working copy loaded
claude plugin validate . && claude plugin validate plugins/golang-architecture --strict
claude plugin eval plugins/golang-architecture --allow-tools Bash Write Edit   # with/without-skill comparison
```

## Releasing

Teammates receive a change only when the plugin's version changes.

1. Bump `version` in `plugins/golang-architecture/.claude-plugin/plugin.json` using semver:
   - **patch:** fixes;
   - **minor:** new conventions or files;
   - **major:** changes that break the layout of existing generated services.
2. Open a PR. CI fails if the plugin changed without a version bump.
3. After merge, tag the release: `claude plugin tag plugins/golang-architecture`.

## Dependencies

Dependabot opens weekly PRs for the template's Go modules and for the CI actions. CI must pass, and the version must be bumped, before a Dependabot PR is merged. When upgrading Go itself, keep the `go` line on the oldest supported release that the dependencies allow.
