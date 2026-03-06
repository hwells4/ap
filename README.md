# ap

Run autonomous agent pipelines from the command line.

```bash
# Run a bug-hunt + security-audit loop, repeated 3 times
ap run "(bug-hunt:3 -> security-audit:1) x3" my-audit
```

This runs `bug-hunt` for 3 iterations, then `security-audit` for 1, and repeats that cycle 3 times — 12 total iterations across 6 stages, fully autonomous.

## Install

```bash
go install github.com/hwells4/ap/cmd/ap@latest
```

## Usage

```bash
# Run a single stage
ap run ralph my-session

# Run a stage for 25 iterations
ap run ralph:25 my-session

# Chain stages together
ap run "improve-plan:5 -> refine-tasks:5" my-session

# Repeat a chain
ap run "(bug-hunt:3 -> audit:1) x3" my-session

# Run a YAML pipeline
ap run ./pipeline.yaml my-session

# Check on a session
ap status my-session
ap logs my-session -f

# Stop and clean up
ap kill my-session
ap clean my-session
```

## Spec Syntax

The first argument to `ap run` is a spec. Parsed in this order:

| Spec | Example | What it does |
|------|---------|--------------|
| Repeat | `"(a:3 -> b:1) x3"` | Repeat a chain N times (`x`, `X`, or `*`) |
| Chain | `"a:5 -> b:5"` | Run stages sequentially |
| YAML | `./pipeline.yaml` | Run a pipeline file |
| Prompt | `./prompt.md` | Run a prompt file directly |
| Stage:N | `ralph:25` | Run a stage for N iterations |
| Stage | `ralph` | Run a stage with default iterations |

Typo-tolerant: `ralph 25` recovers to `ralph:25`, `a > b` recovers to `a -> b`.

## Stages

Stages are reusable prompt + config bundles. Builtins are embedded; project stages live at `.claude/stages/{name}/`:

```
.claude/stages/my-stage/
  stage.yaml
  prompt.md
```

`ap list` shows all available stages. Project stages override builtins.

## Commands

| Command | Description |
|---------|-------------|
| `ap run <spec> <session>` | Start a session |
| `ap list` | List available stages |
| `ap status <session>` | Check session state |
| `ap logs <session> [-f]` | Read/follow session events |
| `ap resume <session>` | Resume paused/failed session |
| `ap kill <session>` | Terminate a session |
| `ap clean <session>` | Remove run artifacts |
| `ap query sessions` | Query sessions across projects |
| `ap watch <session> --on <event> <cmd>` | Trigger hooks on events |

All commands support `--json` for machine output.

## Providers

| Provider | Aliases | Default |
|----------|---------|---------|
| `claude` | `anthropic` | Yes |
| `codex` | — | No |

```bash
ap run ralph:10 my-session --provider codex -m o4-mini
```

## How It Works

Each session runs in tmux (or as a subprocess). Every iteration: generate context -> run the agent -> extract the result -> check termination -> repeat or stop.

State lives in SQLite (`.ap/ap.db`). Sessions can be `running`, `paused`, `failed`, `completed`, or `aborted`. Failed/paused sessions can be resumed with `ap resume`.

For the full agent contract — context.json schema, template variables, signal formats, termination strategies, event types — see [AGENTS.md](AGENTS.md).

## Testing

```bash
go test ./...
```
