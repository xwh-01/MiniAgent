# go-code-agent

A Go-native coding agent runner inspired by the minimal bash-first design of mini-swe-agent.

This M7 scaffold supports:

- single binary CLI
- optional `run` subcommand plus backwards-compatible bare flags
- YAML/JSON config files, with automatic `codeagent.yaml` discovery
- global `.env` plus local `.env` loading
- OpenAI-compatible chat completions
- Anthropic Messages API
- replay model for deterministic tests/demos
- human model for manual agent turns
- bash fenced-block action parsing
- local shell execution
- Docker sandbox execution
- per-command timeout
- simple dangerous-command policy
- JSON trajectory output
- `git diff --binary` patch export
- benchmark task specs in YAML/JSON
- concurrent benchmark execution
- per-task `trajectory.json`, `patch.diff`, `result.json`, and top-level `summary.json`
- `codeagent inspect` for trajectory browsing
- `codeagent report` for benchmark summaries plus `report.json` and `report.html`
- `codeagent swebench` compatibility commands for import/run/prediction export
- `codeagent serve` HTTP worker mode for async agent jobs

## Build

```bash
go build ./cmd/codeagent
```

## Configuration priority

Configuration resolves in this order:

```text
CLI flags > YAML/JSON config > process env / .env > defaults
```

The runner loads these env files before building defaults:

```text
$CODEAGENT_GLOBAL_CONFIG_DIR/.env
# or, by default:
~/.config/go-code-agent/.env

./.env
```

Existing process environment variables are not overwritten by `.env` files.

## Run one task locally with flags

```bash
export OPENAI_API_KEY=...
go run ./cmd/codeagent \
  --task "Fix the bug in this repo and run tests" \
  --workspace /path/to/repo \
  --model gpt-4.1-mini \
  --out runs/bugfix-001
```

The explicit subcommand also works:

```bash
go run ./cmd/codeagent run --task "Inspect this repo" --workspace .
```

## Run with YAML config

```bash
go run ./cmd/codeagent \
  --config configs/default.yaml \
  --task-file issue.md \
  --workspace /path/to/repo
```

A minimal config looks like this:

```yaml
workspace: .
out: runs/latest

agent:
  max_steps: 50
  command_timeout: 120s
  max_observation_chars: 20000

model:
  provider: openai-compatible
  model: gpt-4.1-mini
  base_url: https://api.openai.com/v1
  api_key_env: OPENAI_API_KEY
  temperature: 0
  max_tokens: 4096

environment:
  type: local
```

If `codeagent.yaml`, `codeagent.yml`, `.codeagent.yaml`, or `.codeagent.yml` exists in the current directory, it is loaded automatically.

The main config YAML parser intentionally supports a small config-oriented subset: nested maps, scalar values, quoted strings, comments, and literal blocks with `|`. It does not support anchors, aliases, or custom tags.

## Run in Docker

The Docker environment starts a long-lived container, bind-mounts your workspace,
and executes every agent command via `docker exec`. By default the container is
removed after the run.

```bash
export OPENAI_API_KEY=...
go run ./cmd/codeagent \
  --config configs/docker.yaml \
  --task-file issue.md \
  --workspace /path/to/repo
```

Equivalent flags:

```bash
go run ./cmd/codeagent \
  --env docker \
  --image python:3.12 \
  --task-file issue.md \
  --workspace /path/to/repo \
  --model gpt-4.1-mini \
  --out runs/docker-001
```

Useful Docker flags:

```bash
--container-name codeagent-debug
--container-workspace /workspace
--keep-container
--command-timeout 2m
```

If a command times out, the runner kills and restarts the container so orphaned
processes do not keep running. Workspace changes survive because the workspace is
bind-mounted from the host.

## OpenAI-compatible gateways

```bash
export OPENAI_API_KEY=...
go run ./cmd/codeagent \
  --base-url http://localhost:8000/v1 \
  --model local-model-name \
  --task-file issue.md \
  --workspace .
```

## Anthropic

```bash
export ANTHROPIC_API_KEY=...
go run ./cmd/codeagent \
  --provider anthropic \
  --model claude-model-name \
  --api-key-env ANTHROPIC_API_KEY \
  --task-file issue.md \
  --workspace .
```

## Replay model

Replay mode is useful for deterministic tests, demos, and parser/environment debugging without an API key.

```bash
go run ./cmd/codeagent --config configs/replay.yaml
```

Replay files can be JSON arrays of strings:

```json
[
  "```bash\nls -la\n```",
  "```bash\necho COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT\n```"
]
```

## Human model

Human mode lets you manually type assistant turns. End your answer with EOF (`Ctrl-D` on Unix shells):

```bash
go run ./cmd/codeagent \
  --provider human \
  --task "Inspect this repo and submit" \
  --workspace .
```

## Benchmark runner

M4 added a small benchmark/evaluation harness:

```bash
go run ./cmd/codeagent bench run \
  --config configs/bench-replay.yaml \
  --out runs/bench-replay \
  examples/bench/simple/task.yaml
```

Benchmark output:

```text
runs/bench-replay/
├── summary.json
└── simple-fix/
    ├── task.json
    ├── trajectory.json
    ├── patch.diff
    ├── result.json
    └── workspace/
```

Run many tasks concurrently:

```bash
go run ./cmd/codeagent bench run \
  --config configs/default.yaml \
  --out runs/my-bench \
  --concurrency 4 \
  tasks/*.yaml
```

You can also pass a directory. The runner expands `*.yaml`, `*.yml`, and `*.json` inside it:

```bash
go run ./cmd/codeagent bench run --config configs/default.yaml tasks/
```

Benchmark flags:

```text
--config            base config file
--out               benchmark output directory
--concurrency       number of tasks to run in parallel
--limit             optional max number of tasks
--continue-on-error continue other tasks if one errors, default true
--keep-workspaces   keep cloned repo workspaces after runs
```

### Task spec

A task can be YAML:

```yaml
id: simple-fix
repo: https://github.com/example/project.git
base_commit: abc123
problem_statement: |
  Fix the failing test and preserve existing behavior.
setup:
  - pip install -e .
test:
  - pytest tests/test_bug.py
```

Or it can use a local workspace, which is copied into the run directory before execution:

```yaml
id: local-task
workspace: /path/to/repo
problem_statement: Fix the bug.
test:
  - go test ./...
```

Supported task fields:

```text
id
repo
base_commit
workspace
problem_statement
problem_statement_file
setup
test
config
```

`setup` and `test` are command lists. The task YAML parser supports scalar values, literal blocks, and simple list items.

Each task writes:

```text
trajectory.json  # full agent trace
patch.diff       # git diff --binary from the workspace
result.json      # task status, test results, timings
```

The benchmark writes:

```text
summary.json     # aggregate resolved/failed/errored counts plus per-task results
report.json      # generated by codeagent report
report.html      # generated by codeagent report
```

A task is considered resolved when the agent submits and all test commands exit with code 0.


## Inspect a trajectory

M5 adds a text trajectory inspector:

```bash
go run ./cmd/codeagent inspect runs/bench-replay/simple-fix/trajectory.json
```

Show one step only:

```bash
go run ./cmd/codeagent inspect --step 2 runs/bench-replay/simple-fix/trajectory.json
```

Show the raw message history:

```bash
go run ./cmd/codeagent inspect --messages runs/bench-replay/simple-fix/trajectory.json
```

Useful inspector flags:

```text
--step N       render only one 1-based step
--messages     show raw conversation messages instead of step view
--full         do not truncate long fields
--max-chars N  max chars per rendered field, default 2000
```

## Generate benchmark reports

M5 also adds a benchmark report command. It reads `summary.json`, prints a terminal summary, and writes `report.json` and `report.html` by default:

```bash
go run ./cmd/codeagent report runs/bench-replay
```

Custom output paths:

```bash
go run ./cmd/codeagent report \
  --json runs/bench-replay/my-report.json \
  --html runs/bench-replay/my-report.html \
  runs/bench-replay
```

Report flags:

```text
--json PATH   optional report JSON path, default <bench-dir>/report.json
--html PATH   optional report HTML path, default <bench-dir>/report.html
--no-json     skip JSON report output
--no-html     skip HTML report output
```


## SWE-bench compatibility layer

M6 adds a lightweight SWE-bench compatibility layer. It is designed for inference/prediction generation, not as a full replacement for the official Docker evaluation harness.

It can read SWE-bench-style JSONL, JSON arrays, or dictionary-shaped JSON files with fields such as:

```json
{
  "instance_id": "django__django-12345",
  "repo": "django/django",
  "base_commit": "abc123",
  "problem_statement": "Issue text...",
  "hints_text": "Optional hints..."
}
```

Convert instances into internal benchmark task specs:

```bash
go run ./cmd/codeagent swebench import \
  --out tasks/swebench \
  --append-hints \
  examples/swebench/instances.jsonl
```

Run a SWE-bench-style JSONL file through the existing benchmark runner:

```bash
go run ./cmd/codeagent swebench run \
  --config configs/default.yaml \
  --out runs/swebench-lite \
  --concurrency 4 \
  --limit 10 \
  --model-name go-code-agent-gpt-4.1-mini \
  swebench-lite.jsonl
```

`repo` values like `django/django` are normalized to GitHub clone URLs. Existing full URLs such as `https://github.com/django/django.git` are preserved.

By default, SWE-bench mode does not run official acceptance tests. It generates patches and predictions. You can add lightweight local post-submit commands with repeated `--test` flags:

```bash
go run ./cmd/codeagent swebench run \
  --config configs/default.yaml \
  --out runs/swebench-smoke \
  --test "python -m pytest" \
  examples/swebench/instances.jsonl
```

Output:

```text
runs/swebench-lite/
├── _tasks/
├── summary.json
├── predictions.json
└── <instance-id>/
    ├── task.json
    ├── trajectory.json
    ├── patch.diff
    ├── result.json
    └── workspace/
```

Build or rebuild predictions from an existing benchmark directory:

```bash
go run ./cmd/codeagent swebench predictions \
  --model-name go-code-agent-gpt-4.1-mini \
  --out runs/swebench-lite/predictions.json \
  runs/swebench-lite
```

Also write JSONL predictions:

```bash
go run ./cmd/codeagent swebench predictions \
  --model-name go-code-agent-gpt-4.1-mini \
  --jsonl runs/swebench-lite/predictions.jsonl \
  runs/swebench-lite
```

The JSON prediction format is dictionary-shaped:

```json
{
  "django__django-12345": {
    "instance_id": "django__django-12345",
    "model_name_or_path": "go-code-agent-gpt-4.1-mini",
    "model_patch": "diff --git ..."
  }
}
```

SWE-bench flags:

```text
codeagent swebench import [flags] <instances.json|instances.jsonl>
  --out PATH
  --task-config PATH
  --append-hints
  --github-base-url URL
  --test CMD             may be repeated

codeagent swebench run [flags] <instances.json|instances.jsonl>
  --config PATH
  --out PATH
  --tasks-dir PATH
  --task-config PATH
  --model-name NAME
  --predictions PATH
  --predictions-jsonl PATH
  --concurrency N
  --limit N
  --continue-on-error
  --keep-workspaces
  --append-hints
  --github-base-url URL
  --test CMD             may be repeated

codeagent swebench predictions [flags] <benchmark-output-dir>
  --model-name NAME
  --out PATH
  --jsonl PATH
```


## HTTP server / worker mode

Start an async worker server:

```bash
go run ./cmd/codeagent serve \
  --config configs/default.yaml \
  --addr :8080 \
  --out runs/server \
  --workers 2
```

Submit a run:

```bash
curl -s http://localhost:8080/v1/runs \
  -H 'content-type: application/json' \
  -d '{
    "task": "Inspect this repo and submit",
    "workspace": "/path/to/repo",
    "model": "gpt-4.1-mini"
  }'
```

Useful endpoints:

```text
GET    /health
POST   /v1/runs
GET    /v1/runs?limit=100
GET    /v1/runs/{id}
DELETE /v1/runs/{id}
GET    /v1/runs/{id}/trajectory
GET    /v1/runs/{id}/patch
GET    /v1/runs/{id}/result
```

A request may override the base server config with fields like `task`, `task_file`, `workspace`, `out`, `env`, `image`, `provider`, `model`, `replay_file`, `max_steps`, and `command_timeout`. Jobs are stored in memory, while artifacts are written under the configured output directory.

## Submit signal

The agent stops when the executed command or its output contains:

```text
COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT
```

Example final assistant response:

````markdown
```bash
echo COMPLETE_TASK_AND_SUBMIT_FINAL_OUTPUT
```
````

## Generated files for single-task runs

```text
runs/latest/
├── trajectory.json
└── patch.diff
```

## Tests

```bash
go test ./...
go build ./cmd/codeagent
```

## Current scope

M7 is still intentionally bash-first. It includes a lightweight SWE-bench prediction layer, but not the full official image-building/evaluation harness. It does not yet include:

- durable job storage
- authentication / authorization for server mode
- remote workers
- advanced tool registry
- resource quota enforcement beyond command timeouts
- rich TUI trajectory browser

Those are natural M7+ additions.
