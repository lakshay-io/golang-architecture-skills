# golang-architecture-skills

Claude Code skills that give every Go backend service the same production-grade architecture. Ask Claude for a new service and you get a compiling, tested skeleton with this stack:

- Gin
- a ports-and-adapters layout
- New Relic
- structured logging
- graceful shutdown

It's architecture only: no business features until you ask for them.

## What's inside

| Plugin | Skill | What it does |
|---|---|---|
| `golang-architecture` | `go-backend-scaffold` | Generates a new Go service on the standard architecture. Adds domain modules the standard way. Audits existing services against the standard. |

A generated service looks like this:

```
cmd/server/          entrypoint: signals, config, exit code
internal/app/        composition root: wiring and graceful lifecycle
internal/config/     typed env config, validated at boot
internal/api/        Gin: router, middleware, error envelope, handlers
internal/domain/     business modules (one package per bounded context)
internal/storage/    database and cache adapters
internal/platform/   apperr, logger, telemetry (New Relic), httpclient, health, requestid
docs/                ARCHITECTURE.md, ADDING_A_MODULE.md, OBSERVABILITY.md
```

It passes `make verify` from the first commit: gofmt, vet, race-enabled tests and build. The full rules are in [ARCHITECTURE.md](plugins/golang-architecture/skills/go-backend-scaffold/assets/template/docs/ARCHITECTURE.md).

## Install (once per developer)

The full guide, covering prerequisites, checking it works, updates, team rollout and troubleshooting, is in **[INTEGRATION.md](INTEGRATION.md)**. The short version:

In Claude Code:

```
/plugin marketplace add lakshay-io/golang-architecture-skills
/plugin install golang-architecture@golang-architecture-skills
```

Or from a terminal:

```sh
claude plugin marketplace add lakshay-io/golang-architecture-skills
claude plugin install golang-architecture@golang-architecture-skills
```

Start a new Claude Code session, or run `/reload-plugins`, to load it.

If you copied `go-backend-scaffold` into `~/.claude/skills/` by hand earlier, delete that copy. Otherwise both versions load.

### Roll it out to a whole repo automatically

Commit this to `.claude/settings.json` in each backend repository. Anyone who opens the repo in Claude Code and trusts the folder is offered the plugin:

```json
{
  "extraKnownMarketplaces": {
    "golang-architecture-skills": {
      "source": { "source": "github", "repo": "lakshay-io/golang-architecture-skills" },
      "autoUpdate": true
    }
  },
  "enabledPlugins": {
    "golang-architecture@golang-architecture-skills": true
  }
}
```

## Use

Just ask; the skill triggers on its own:

- *"Create a new Go service called notification-service."*
- *"Set up our standard Go architecture in this repo."*
- *"Add an orders module to this service."*
- *"Check this Go service against our architecture standard."*

Or invoke it explicitly with `/golang-architecture:go-backend-scaffold`.

**Requirements:** Python 3, `make` and Go. Any recent Go works: the project pins Go 1.26 and downloads that toolchain automatically. New Relic switches on when `NEW_RELIC_LICENSE_KEY` is set, and is required when `APP_ENV=production`.

## Updates

Releases are versioned (`version` in [plugin.json](plugins/golang-architecture/.claude-plugin/plugin.json)), so you get a change only when its version is bumped. To update:

- **Automatically:** `/plugin` → Marketplaces → `golang-architecture-skills` → Enable auto-update.
- **Manually:** `claude plugin update golang-architecture@golang-architecture-skills`, then restart Claude Code.

## Security

Installing a plugin means trusting its contents. This skill runs `scripts/scaffold.py` and `make` on your machine with your permissions. Every change to `main` goes through review and CI, and the code owners are in [.github/CODEOWNERS](.github/CODEOWNERS). Review the release before you update.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).
