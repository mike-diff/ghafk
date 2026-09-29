# Troubleshooting

Start with `ghafk status`. It shows the timer, the recent log, the GitHub
account that ghafk uses, and the state of each repository. For more of the
log, use:

- Linux: `journalctl --user -u ghafk.service`
- macOS: `~/Library/Logs/ghafk.log`

## Nothing happens

| Cause | Fix |
|---|---|
| The timer does not run. | Run `ghafk start`. Then look for the next run in `ghafk status`. |
| `ghafk tick` or `ghafk start` says that an engine runs on this machine. | An engine account from the deprecated setup works the repositories. Run `ghafk engine remove --purge`, then `ghafk start`. |
| `ghafk status` says that you cannot read the files of the engine. | Setup added you to the group of the engine. On Linux, log out and log in again. On macOS, open a new terminal. |
| `ghafk engine setup` says that an account exists with a different home. | A manual setup uses that account. The engine account is deprecated: remove its timer and the account, then run `ghafk start`. |
| The repository is not registered. | Run `ghafk init` in the repository. Commit and push `.ghafk/WORKFLOW.md`. |
| `ghafk status` does not show a repository that you pushed. | The token of your `gh` login does not include the repository, a `skip` line names it, or an organization owns it. See [repositories](configuration.md#repositories). |
| `ghafk status` shows `not ready` for the repository. | Read the reason. Usually `.ghafk/WORKFLOW.md` is missing or has no worker, and no machine default is set. |
| The issue parked with "100 or more comments". | GitHub gives ghafk only the first 100 comments. Open a new issue that links to the old one. |
| The issue has no `agent` label. | Add the label, or comment `/start`. |
| Somebody is assigned to the issue. | ghafk ignores assigned issues. Remove the assignee. |
| A person without write access opened the issue. | A maintainer must comment `/start`. |
| ghafk works on a different issue. | ghafk does one step for each repository on each tick. Open pull requests go first, then the issue with the lowest number. Wait for the next tick. |

## The issue has the `needs-human` label

ghafk parked the issue. Read the last comment from ghafk. It tells you what
happened and which command continues the work.

| Message | Fix |
|---|---|
| The groomer needs your answer. | Reply `/answer <choice>`. |
| No checks are configured for this repository. | Add `checks` to `.ghafk/WORKFLOW.md`. Then reply `/retry`. |
| The checks failed twice. | Read the failure output in the comment. Correct the issue or the checks. Then reply `/retry`. |
| The judge rejected the diff twice. | Read the rejection. Make the issue clearer. Then reply `/retry`. |
| The worker made no changes. | The issue is probably not clear, or the change exists already. Correct the issue. Then reply `/retry`. |
| The change touches paths ghafk never merges on its own. | The pull request changes `.github/` or `.ghafk/`. Review it and merge it yourself. |
| The change touches `.github/`, which GitHub Actions runs with this repository's secrets as soon as a branch is pushed. | The agent changed a workflow. ghafk did not push it. The comment shows the change; apply it yourself, or `/close` the issue. |
| GitHub refused the merge. | Read [Merge is refused](#merge-is-refused). |
| The reconciler says the landed code already satisfies this contract. | Close the issue, or reply `/retry` if the work is not complete. |

## Merge is refused

GitHub refuses a merge that breaks a branch protection rule.

- **Required status checks:** ghafk merges immediately after its own checks.
  A required check that is still running causes the refusal. Remove the
  requirement, or merge the pull request yourself.
- **Required reviews:** without a [GitHub App](github-app.md), the judge
  cannot approve a pull request that you opened. Set up the app, or approve
  the pull request yourself.

## Errors on the machine

| Error | Fix |
|---|---|
| `ghafk: command not found` | Add `$(go env GOPATH)/bin` to your `PATH`. |
| The agent command is not found during a tick, but works in your shell. | The timer uses the `PATH` from the time of `ghafk start`. Run `ghafk start` again. |
| The push fails during a tick. | The timer has no SSH agent. Run `gh auth setup-git` and use HTTPS. |
| `config: ...` | A value in `~/.ghafk/config` is not correct. Read the message and correct the line. See [machine settings](configuration.md#machine-settings). |
| The log shows that ghafk cannot get an app token. | ghafk uses your login instead. Look at the App ID, the key and the installation. See [Run as a GitHub App](github-app.md). |
| Each tick fails with `Bad credentials` or `401`. | The GitHub token of your `gh` login expired. `ghafk status` shows the expiry date. Run `gh auth login` again. With a deprecated engine account, run `ghafk engine token`, or see [Replace the token before it expires](separate-user.md#replace-the-token-before-it-expires). |
| The log shows a warning that the GitHub token expires soon. | Replace the token before the date in the warning. |
| `ghafk harness test` fails. | Log in to the agent CLI, and look at its model name. Then run the test again. |
| `the sandbox needs bubblewrap` or `the sandbox does not start` on Linux. | Install the `bubblewrap` package. On stock Ubuntu 24.04, ghafk prints the one-time `sudo` step that installs an unconfined bubblewrap AppArmor profile, which lets bubblewrap create user namespaces; run it, then `ghafk tick` again. |
| A run parks because the change holds `a value that looks like a secret`. | The comment names the file and line. If the value is a real secret, rotate it, and remove it from the issue text or the file that the agent copied it from. If it is a test value, add its path to a `secrets-allow:` line in `.ghafk/WORKFLOW.md`. Then reply `/retry`. |
| A run parks with `the sandbox proxy denied: <hosts>`, or the tick log shows it. | The agent or the checks needed a host outside the allowlist. For one repository, add the host with an `egress:` line in `.ghafk/WORKFLOW.md`. For a model provider that every run uses, add it to an `egress:` line in `~/.ghafk/config`. Then reply `/retry`. |
| A harness that uses a model server on this machine (for example `localhost:11434`) fails inside the sandbox. | The sandbox has no network of its own and the proxy refuses local addresses. Add the port to a `local:` line in `~/.ghafk/config`, for example `local: 11434`. See [machine settings](configuration.md#machine-settings). |
| A run refuses to start with `omp cannot run inside the ghafk sandbox`. | omp's built-in model client ignores the egress proxy, so it can never reach its model inside the sandbox. Switch the repository or machine default to `pi`, or an omp version whose client honors `HTTPS_PROXY`. |
| A run parks because a checks program is `not on PATH`. | Install it, or add its directory with a `bind:` line in `~/.ghafk/config`, then reply `/retry`. ghafk looks for the program in `bind:` directories too. |
| A run parks because the harness program `does not start inside the sandbox`. | The program resolves on your `PATH` but its real files live outside the bound directories. Install it system-wide, or bind the directory it needs (not your whole home) with a `bind:` line, then reply `/retry`. |
| A run needs a secret that ghafk does not pass, for example `NPM_TOKEN` for a private registry. | Add it to an `env:` line in `~/.ghafk/config`. Harness API keys pass automatically for the built-in harness profiles; a custom profile needs an `env:` line and an `egress:` line. See [machine settings](configuration.md#machine-settings). |
| A run parks with `claude keeps no token ghafk can pass into the sandbox`. | Run `claude setup-token` once and put `CLAUDE_CODE_OAUTH_TOKEN=<token>` in `~/.ghafk/env` with mode `0600`. |
| A run parks with `codex has no login file`. | Codex stores its login elsewhere, for example the keyring. Run `codex login` as yourself so `~/.codex/auth.json` exists. |
| A run parks with `the agent changed the worktree's git pointer`. | The agent rewrote the `.git` file of its worktree. Reply `/retry` to start a clean run; report the issue if it repeats. |
| `ghafk start` refuses because a system engine still runs. | Run `ghafk engine remove --purge`, then `ghafk start`. See [the engine account](security.md#the-engine-account-deprecated). |
