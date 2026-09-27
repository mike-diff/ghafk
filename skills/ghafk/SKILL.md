---
name: ghafk
description: Install, configure and operate ghafk, the CLI that works GitHub issues labeled "agent" with a coding agent on a timer and merges the pull requests. Use when the user wants to set up ghafk, register or remove a repository, choose a harness and model, change the tick interval or the progress table, steer an issue with slash commands, or find out why ghafk parked, skipped or did not merge an issue.
---

# ghafk

ghafk runs on the user's machine. A timer runs `ghafk tick`. Each tick does
one step for each registered repository: continue an open ghafk pull
request, or groom and implement the lowest-numbered issue with the `agent`
label. GitHub holds all state. Do not look for a database or a server.

## Before you change anything

1. Run `ghafk status`. It shows the timer, the log, the GitHub account and
   the state of each repository.
2. Run `ghafk help` to see the commands of the installed version.

## Set up

```sh
go install github.com/mike-diff/ghafk@latest
export PATH="$PATH:$(go env GOPATH)/bin"
ghafk harness list
ghafk harness default <harness> <model>
ghafk harness test <harness> <model>
cd <repository> && ghafk init
git add .ghafk/WORKFLOW.md && git commit -m "chore: configure ghafk" && git push
ghafk start
```

Requirements: Linux with systemd or macOS, Go 1.26 or newer, `git` with a
commit identity, `gh` logged in, and one supported agent CLI.

To keep agents away from the user's keys and other repositories, run ghafk
as its own Linux user with a fine-grained token. Follow
`docs/separate-user.md`. Then install new versions and run `ghafk status`
as that user, not as the user's own account. To add a repository later,
run `ghafk init` in the user's own clone and push the workflow file. The
engine finds each owned repository with `.ghafk/WORKFLOW.md` on its default
branch.

## Configuration

| File | Holds |
|---|---|
| `<repository>/.ghafk/WORKFLOW.md` | `label`, `checks`, `worker`, `groomer`, `judge`, `reconciler`, `timeout`, then prompt text. `checks` is necessary for a merge. |
| `~/.ghafk/config` | `default: <harness> <model>`, `progress: <columns>`, `interval: <minutes>`, `skip: <owner/name>` |
| `~/.ghafk/repos` | Registered repository paths. Use `ghafk init` and `ghafk remove`. ghafk also works each owned repository with `.ghafk/WORKFLOW.md` on its default branch, cloned into `~/.ghafk/clones/`. |
| `~/.ghafk/harnesses` | Extra or changed harness profiles: `name: parser command {model}`. |
| `~/.ghafk/prompts/<role>.md` | Replaces the built-in prompt of `groom`, `worker`, `judge`, `reconcile` or `common`. It must keep the answer format of the built-in file, or ghafk parks the issue. |

- `progress` columns are `step status started ended duration tokens
  harness`. The default is `step status duration tokens harness`. `step`
  is necessary. Remove `harness` to hide the harness and model on cards.
- `interval` is minutes between ticks and must divide 60. After a change,
  run `ghafk start` again.
- `worker` and `checks` run through `sh -c`. Show the user each change to
  them before you commit it.

## Steer from GitHub

A comment that starts with a command steers ghafk. Only people with write
access can use commands.

| Command | Result |
|---|---|
| `/start` | Starts work on the issue. |
| `/answer <text>` | Answers the question from the groomer. |
| `/retry` | Continues a parked issue or pull request. |
| `/close` | Closes the issue or the pull request. |
| `/stop` | Removes the labels. ghafk ignores the issue. |

## Diagnose

- **Issue not started:** check for the `agent` label, an assignee (ghafk
  ignores assigned issues), and a registered repository. An issue from a
  person without write access needs `/start` from a maintainer.
- **Issue has `needs-human`:** read the last ghafk comment. It names the
  cause and the command that continues the work.
- **Merge refused:** a branch protection rule blocks it. ghafk merges
  immediately after its local checks, so required status checks cause a
  refusal.
- **Agent not found during a tick:** the timer keeps the `PATH` from
  `ghafk start`. Run `ghafk start` again.
- **Every tick fails with `Bad credentials` or `401`:** the GitHub token
  expired. `ghafk status` shows its expiry date. The tick log warns in the
  last 14 days. Replace the token as `docs/separate-user.md` describes.
- **Logs:** `journalctl --user -u ghafk.service` on Linux,
  `~/Library/Logs/ghafk.log` on macOS.

## Rules

- Do not push to the default branch of a registered repository while a
  tick runs. The push can race the merge.
- Do not add `GH_TOKEN` to make ghafk work. ghafk uses the stored `gh`
  login or its GitHub App.
- ghafk runs agents with the user's credentials. Register only
  repositories where the user trusts everyone who can label issues.
