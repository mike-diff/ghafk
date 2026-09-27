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
| The repository is not registered. | Run `ghafk init` in the repository. |
| `ghafk status` shows `not ready` for the repository. | Read the reason. Usually `.ghafk/WORKFLOW.md` is missing or has no worker, and no machine default is set. |
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
| Each tick fails with `Bad credentials` or `401`. | The GitHub token expired. `ghafk status` shows the expiry date. Replace the token. See [Replace the token before it expires](separate-user.md#replace-the-token-before-it-expires). |
| The log shows a warning that the GitHub token expires soon. | Replace the token before the date in the warning. |
| `ghafk harness test` fails. | Log in to the agent CLI, and look at its model name. Then run the test again. |
