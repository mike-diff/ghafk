# Configuration

This page lists every command, setting, label and file that ghafk uses.

To limit access to your personal files, run the engine as
[its own account](separate-user.md).

- [Commands](#commands)
- [Repositories](#repositories)
- [Machine settings](#machine-settings)
- [WORKFLOW.md](#workflowmd)
- [Labels](#labels)
- [Harness profiles](#harness-profiles)
- [Files](#files)

## Commands

| Command | What it does |
|---|---|
| `ghafk init [path]` | Registers a repository. Writes `.ghafk/WORKFLOW.md` if the file does not exist. Creates the `agent` label and one `harness:<name>` label for each installed harness. |
| `ghafk remove [path]` | Unregisters a repository. Keeps its files, labels and pull requests. Lists the work that is still in progress. It cannot remove a repository that ghafk found on GitHub. See [repositories](#repositories). |
| `ghafk engine setup` | Runs the engine as a separate system account. Asks for sudo, creates the account, installs the binary and a service, copies your `config`, `harnesses`, `prompts` and App files, and asks for a GitHub token. Run it again to apply changes to these files or after you install a harness. See [the engine account](separate-user.md). |
| `ghafk engine token` | Replaces the GitHub token of the engine. |
| `ghafk engine start`, `ghafk engine stop` | Turns the timer of the engine on or off. |
| `ghafk engine remove [--purge]` | Removes the service and the binary of the engine. `--purge` also deletes the account and its home. |
| `ghafk update` | Installs the newest ghafk from `main`. If an engine exists, it then runs `ghafk engine setup` with the new binary. |
| `ghafk start` | Installs and starts the timer, with the `interval` from the [machine settings](#machine-settings). On Linux, it writes systemd user units. On macOS, it writes a launchd agent. |
| `ghafk stop` | Stops the timer. A tick that is running finishes first. On macOS, the command waits for that tick. |
| `ghafk status` | Shows the timer, the recent log, the expiry date of the GitHub token, the GitHub account that ghafk uses, and the state of each repository. |
| `ghafk tick` | Runs one tick now, in the foreground. |
| `ghafk harness list` | Lists the harness profiles, shows which are installed, and shows the machine default. |
| `ghafk harness default <harness> <model>` | Sets the machine default worker in `~/.ghafk/config`. |
| `ghafk harness test <harness> [model]` | Runs a harness in a scratch repository and reports the result of each check. |

> [!NOTE]
> The timer uses the `PATH` that you have when you run `ghafk start`.
> Run `ghafk start` again after you install a new version of ghafk or a
> new agent CLI.

`ghafk init` sets `checks` from `go.mod`, the JavaScript lockfile,
`Cargo.toml` or `pyproject.toml`. If it cannot find one of these files, it
shows a warning. Then you must write `checks` yourself.

## Repositories

On each tick, ghafk works two sets of repositories:

1. The paths in `~/.ghafk/repos`. `ghafk init` adds a path.
2. Each repository that the `gh` login owns, where `.ghafk/WORKFLOW.md` is
   on the default branch. ghafk finds these on GitHub. It clones a new one
   into `~/.ghafk/clones/<owner>/<name>`. It ignores forks and archived
   repositories.

The second set lets ghafk run as [its own account](separate-user.md) that
cannot read your clones. Run `ghafk init` in your own clone, then commit
and push the workflow file. The engine finds the repository on its next
tick. It needs access to the repository:

- The token of its `gh` login must include the repository. A fine-grained
  token with "All repositories" includes each new repository. It also
  gives the agents write access to every repository that you own.
- The [GitHub App](github-app.md) must be installed on the repository. If
  it is not, ghafk acts as the `gh` login on that repository.

ghafk does not find repositories of organizations. Add a `repo` line to
the [machine settings](#machine-settings), or register them with
`ghafk init`.

To stop work on a repository that ghafk found, delete `.ghafk/WORKFLOW.md`
from its default branch. To keep the file and stop the work on this
machine, add a `skip` line to the [machine settings](#machine-settings).

## Machine settings

The file `~/.ghafk/config` holds the settings for all repositories on this
machine. Each setting is one `key: value` line.

```
default: <harness> <model>
progress: step status duration tokens
interval: 5
```

| Key | Default | Meaning |
|---|---|---|
| `default` | none | The harness and model for repositories that do not set `worker`. `ghafk harness default` writes this line. |
| `progress` | `step status duration tokens harness` | The columns of the progress table on each card, in order. Available columns: `step`, `status`, `started`, `ended`, `duration`, `tokens`, `harness`. `step` is necessary. To keep your harness and model private, remove `harness`. |
| `interval` | `2` | The number of minutes between ticks. The value must divide 60, for example 1, 2, 5, 10, 15, 30 or 60. |
| `repo` | none | A repository, as `owner/name`, that ghafk works although it cannot find it on GitHub, for example a repository of an organization. Write one line for each repository. |
| `skip` | none | A repository, as `owner/name`, that ghafk must not find on GitHub. Write one line for each repository. It does not affect a path in `~/.ghafk/repos`. |

> [!IMPORTANT]
> After you change `interval`, run `ghafk start` again. A change to
> `progress` applies to the next card update.

If a value is not correct, `ghafk start` and `ghafk tick` stop and show
the problem.

## WORKFLOW.md

Each registered repository has the file `.ghafk/WORKFLOW.md`. On each tick,
ghafk reads the file from the default branch on GitHub. A change applies
after you push it. If the clone has no `origin/HEAD`, ghafk reads the file in
the clone. The file
starts with a block of `key: value` lines. ghafk adds the text after the
block to each prompt, after the built-in instructions. Use it for the rules
of your repository. See [prompts](prompts.md).

```markdown
---
label: agent
checks: go build ./... && go vet ./... && go test ./...
timeout: 30
---
Use the standard library only.
Do not change the public API unless the issue asks for it.
```

| Key | Default | Meaning |
|---|---|---|
| `label` | `agent` | The label that tells ghafk to work an issue. |
| `checks` | none | The shell command that must pass before a merge. The limit is 15 minutes. If you do not set it, each pull request parks. |
| `worker` | machine default | `<harness> <model>`, or a shell command. The worker implements the change and repairs it. |
| `groomer` | `worker` | Writes the contract, or asks you one question. |
| `judge` | `worker` | Approves or rejects the diff. We recommend a different model from the worker. |
| `reconciler` | `groomer` | Checks the other open issues again after a merge. |
| `timeout` | `30` | The number of minutes that each agent run can take. |

Each value must be on one line. ghafk ignores keys that it does not know.

> [!IMPORTANT]
> `worker` and `checks` run through `sh -c`. Treat this file as a program.
> Review each change to it. ghafk does not merge a change to `.ghafk/`.

## Labels

| Label | Meaning |
|---|---|
| `agent` | Work this issue. You can change the name with `label`. |
| `needs-human` | ghafk stopped. It waits for you. |
| `ghafk:working` | A run on this issue is in progress. |
| `harness:<harness>` | Use a different harness for the worker on this issue. |
| `model:<id>` | Use a different model for the worker on this issue. |

`harness:` and `model:` change only the worker and its repairs. The
groomer, the judge and the checks do not change. If ghafk cannot use the
label, it parks the issue and gives the reason.

## Harness profiles

A profile is a command template and a parser. The template has a
`{model}` placeholder. The parser reads the final reply of the agent and
its token usage.

The built-in profiles are `pi`, `omp`, `claude`, `codex` and `sesh`. To
add or change a profile, write one line for each profile in
`~/.ghafk/harnesses`:

```
# name: parser command
llm: text llm -m {model}
claude.allow: sonnet opus
```

| Parser | Reads |
|---|---|
| `pi-json` | The JSON output of `pi` and `omp`. |
| `claude-json` | The JSON output of `claude`. |
| `codex-json` | The JSON output of `codex`. |
| `text` | Standard output as the reply. It does not report usage. |

A `name.allow:` line sets the models that a `model:` label can select for
that profile. A model name must start with a letter or a digit. It can
contain `A-Z a-z 0-9 . _ : / @ -`.

## Files

With `ghafk engine setup`, the engine keeps these files in the home of its
account: `/var/lib/ghafk` on Linux and `/usr/local/var/ghafk` on macOS.

| Path | Contents |
|---|---|
| `~/.ghafk/repos` | The registered repository paths, one on each line. `#` starts a comment. |
| `~/.ghafk/clones/` | The clones of the repositories that ghafk found on GitHub. |
| `~/.ghafk/config` | The [machine settings](#machine-settings). |
| `~/.ghafk/env` | Optional. `KEY=value` lines, mode `0600`. `GH_TOKEN` is the token for the `gh` calls and pushes of ghafk. It never goes to agents or checks. Other keys, for example the API key of a harness, go to the agents. If the file does not exist, ghafk uses the `gh` login. |
| `~/.ghafk/harnesses` | Your harness profiles. |
| `~/.ghafk/prompts/` | Your replacements for the built-in [prompts](prompts.md). |
| `~/.ghafk/app`, `~/.ghafk/app.pem` | The App ID and private key of an optional [GitHub App](github-app.md). |
| `~/.ghafk/work/` | The worktrees of runs that are in progress. |
| `~/.ghafk/status.json` | The result of the last tick: time, error, token expiry, account, repositories and installed harnesses. ghafk writes it at the end of each tick, also when the tick fails. |
| `~/.ghafk/tick.log` | The output of the ticks. At 1 MB, ghafk moves it to `tick.log.1` and starts a new file. |
| `~/.config/systemd/user/ghafk.{service,timer}` | Linux: the units that `ghafk start` writes. To uninstall ghafk, run `ghafk stop` and delete them. |
| `~/Library/LaunchAgents/ghafk.tick.plist` | macOS: the agent that `ghafk start` writes. To uninstall ghafk, run `ghafk stop` and delete it. |
| `~/Library/Logs/ghafk.log` | macOS: the tick log. `ghafk status` shows its last 20 lines. |
