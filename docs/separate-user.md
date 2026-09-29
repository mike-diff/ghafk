# The engine account

> **Deprecated.** ghafk now runs as you and puts each agent run and each
> checks run in an OS sandbox. Use `ghafk start`. See
> [security](security.md). This page stays for machines that still run
> an engine account. To change, run `ghafk engine remove --purge`, then
> `ghafk start`.

Use a dedicated account to limit what coding agents can access.
The account is separate from your GitHub account and GitHub App.

## Why use a separate account

Agents have the file permissions of the account that runs them.
Your personal account can expose SSH keys, cloud credentials and unrelated
repositories.
Docker group membership gives access that is equivalent to root access.

A dedicated account prevents access to personal files that their permissions
protect.
It does not provide a complete sandbox.
Agents can still use that account's credentials, network access and files.
They can access each repository that its GitHub token permits.

Read the [security model](security.md) before you continue.

## Automatic setup

Run this command from your own account:

```sh
ghafk engine setup
```

The command asks for your sudo password once. Then it does these steps:

1. It creates a system account: `ghafk` with the home `/var/lib/ghafk` on
   Linux, or the hidden account `_ghafk` with the home
   `/usr/local/var/ghafk` on macOS.
2. It adds you to the group of the account, so that you can see its status.
3. It installs the ghafk binary at `/usr/local/bin/ghafk`. Root owns the
   binary.
4. It copies your `config`, `harnesses`, `prompts` and GitHub App files.
5. It asks for a fine-grained GitHub token. It shows a link that fills in
   the permissions. Select the repositories that ghafk works. The token
   must belong to the account that opens and merges the pull requests.
6. It installs a systemd service on Linux, or a LaunchDaemon on macOS, and
   starts the timer.
7. It looks for harnesses that the engine can use.

The command never installs a harness. To install one, enter the account in
a new terminal, install the harness and log in to it, then close that
terminal:

```sh
sudo -iu ghafk      # on macOS: sudo -iu _ghafk
```

Run `ghafk engine setup` again after that. Also run it again after you
change your `~/.ghafk` files.

Use these commands from your own account:

| Task | Command |
|---|---|
| See the engine | `ghafk status` |
| Update the engine | `ghafk update`, then `ghafk engine setup` to install the new binary for the engine |
| Replace the GitHub token | `ghafk engine token` |
| Stop or start the timer | `ghafk engine stop`, `ghafk engine start` |
| Remove the engine | `ghafk engine remove`, or `ghafk engine remove --purge` to also delete the account |

After setup, log out and log in again on Linux, or open a new terminal on
macOS. Then `ghafk status` can read the files of the engine.

The engine cannot use programs in your home directory. Install `gh` and
`git` system-wide.

## Manual setup

The sections below set up the account by hand. Use them only when
`ghafk engine setup` cannot do the work, for example on a Linux system
without systemd.

## 1. Check the existing installation

1. Record the current timer state.
2. Check the owner and mode of your personal home directory.
3. Check its access control lists.
4. Check the locations of Go, Git and GitHub CLI.
5. Make sure other users can execute those programs.
6. Record the agent package name and version.
7. Check whether the dedicated account already exists.

Do not display credentials or the configured model.
For secret files, record only the file name, owner and mode.
Stop if a check fails.

## 2. Create the Linux account

Run these commands from your personal account if the dedicated account does
not exist:

```sh
sudo useradd --create-home --shell "$(command -v bash)" ghafk
sudo loginctl enable-linger ghafk
```

Lingering keeps the user service manager available after logout.

Do not add this account to supplementary groups.
Do not give it sudo access.
Do not give it access to Docker or another privileged container service.

Check which services need your personal home directory before you restrict
access.
Remove access for other users if necessary.
Mode `0700` permits access only to the owner.
Mode `0750` also permits access to the owning group.
Use `0750` only if the dedicated account cannot use that group.
Remove any access control entry that gives the dedicated account access.
Do not change permissions recursively.

## 3. Configure a restricted GitHub login

1. Create a fine-grained personal access token in your personal GitHub account.
2. Select the repositories that ghafk must operate on, or "All repositories".
3. Set Contents, Issues and Pull requests to Read and write.
4. Keep Metadata at Read-only.
5. Set a suitable expiration date.

With selected repositories, you must add each new repository to the token.
With "All repositories", you do not change the token for a new repository.
But the agents can then write to every repository that you own.

Use the account that must appear as the author and merger of pull requests.
The GitHub App does not replace this login.

Enter the dedicated account in an interactive terminal:

```sh
sudo -iu ghafk
```

Do not nest a quoted shell command inside this command.
An extra shell can expand variables before the intended command runs.

1. Save the token in a temporary file with mode `0600`.
2. Make the dedicated account the file owner.
3. Run `gh auth login --hostname github.com --git-protocol https --with-token`
   with input redirected from that file.
4. Delete the temporary file after login succeeds.
5. Run `gh auth setup-git`.
6. Set `git config --global user.name` to your commit name.
7. Set `git config --global user.email` to your verified GitHub email address.

Use a hidden input prompt when you save the token.
Do not put the token in a command argument, shell history or chat message.
GitHub CLI retains a credential after you delete the temporary file.
The dedicated account can read or use that credential.

### Replace the token before it expires

`ghafk status` shows the expiry date of the token.
In the last 14 days, each tick writes a warning to the log.
After the token expires, each tick fails until you replace it.

1. Create a new token with the same repositories and permissions.
2. Log in again with the steps above.
3. Run `ghafk status` and make sure that it shows the new expiry date.
4. Delete the old token on GitHub.

With selected repositories, add each new repository to the token.

## 4. Install the programs and repositories

Run all installation commands as the dedicated account.

1. Install a supported Node version in this account if the agent requires Node.
2. Install pnpm if you use Pi.
3. Install the recorded Pi package version with pnpm.
4. Review dependency install scripts before you approve them.
5. Configure the agent with a separate API key if possible.
6. Install ghafk with the Go command in the [quick start](../README.md#quick-start).
7. Add the account's Go executable directory to its login `PATH`.
8. Make sure its login `PATH` also contains the agent and its runtime.
9. Clone each selected repository over HTTPS with `gh repo clone`.

Do not use npm or npx for the Pi installation.
Do not reuse programs from the personal account's home directory.

If you must reuse Pi authentication, obtain approval before you copy its
authentication file.
Copy only that file.
Set its owner to the dedicated account.
Set its mode to `0600`.
Do not copy settings, extensions or sessions.
The copied credential remains shared with the personal account.

## 5. Transfer the machine configuration

Use an administrator only for files that the dedicated account cannot read.
The [file reference](configuration.md#files) identifies the files below.

1. Create the dedicated account's ghafk configuration directory with mode `0700`.
2. Set the directory owner to the dedicated account.
3. Copy the App ID file named `app`.
4. Copy the App private key named `app.pem`.
5. Set both file owners to the dedicated account.
6. Set both file modes to `0600`.
7. Copy the `default:` and `interval:` settings from the original `config` file.
8. Set the `progress:` line as shown below.
9. Add a `skip:` line for each repository that has `.ghafk/WORKFLOW.md` but
   that this account must not work.
10. Set the owner of the `config` file to the dedicated account.
11. Set its mode to `0600`.

Do not write a `repos` file.
The engine finds each repository that you own with `.ghafk/WORKFLOW.md` on
its default branch.
It clones the repository into its own home directory.

```text
progress: step status duration tokens
```

Keep the default interval if the original file has no `interval:` line.
The progress setting omits the harness and model column from public cards.
It does not remove secrets from agent output.

Do not copy the original `harnesses` file.
Review any repository settings that override the machine default.
Do not run `ghafk init` for these existing repositories.
Their labels already exist.
The init command can publish labels for local harness profiles.

See [GitHub App configuration](github-app.md) for the required App permissions.

## 6. Verify before you start the timer

Run these checks as the dedicated account:

1. Run `id`.
2. Confirm that it has no supplementary groups.
3. Confirm that it cannot read your SSH directory.
4. Confirm that it cannot read your personal GitHub CLI configuration.
5. Confirm that it cannot read your personal repository clones.
6. Confirm that it cannot read your personal ghafk configuration.
7. Check the owner and mode of each copied secret file.
8. Check that `gh auth status` succeeds.
9. Check access to each selected repository with `gh repo view`.
10. Check that `gh repo view` fails for a known private repository outside the selection.
11. Run `ghafk harness test` with the harness and model from the default setting.
12. Confirm that every harness check passes.
13. Check that `ghafk status` reports every repository as ready.
14. Check that the engine account is your GitHub App's bot account.

Use an account with access to confirm that the negative-test repository exists
and is private.
Public repositories are not suitable for this test.
A restricted token can still read public repositories.
A network error does not prove that access is restricted.

Keep authentication output, harness diagnostics and status logs private.
Report only the necessary check results.
Stop if any check fails.
Do not bypass a failed check.

## 7. Switch the timers

> [!WARNING]
> Never run both timers at the same time.
> Two engines can work on the same issue.

ghafk commands find the user service manager themselves.
Before your own `systemctl --user` commands, set the runtime directory in each
account's login shell:

```sh
export XDG_RUNTIME_DIR="$(loginctl show-user "$(id -u)" -p RuntimePath --value)"
```

Use the original account first.
Check that its service has not failed.
Wait for the current tick to finish:

```sh
until [ "$(systemctl --user is-active ghafk.service)" = inactive ]; do
  sleep 2
done
```

Run `ghafk stop` as the original account.
Repeat the wait command after the timer stops.
A tick can start between the first check and the stop command.
Confirm that the original timer is inactive and disabled.
Confirm that its service is inactive.

Enter the dedicated account with `sudo -iu ghafk`.
Set its runtime directory with the command above.
Run `ghafk start`.
Run `ghafk status`.
Confirm that its timer is active.
Confirm that each repository is ready.
Confirm that the engine account is the expected bot.

The start command records the current `PATH` in the user service.
Use the account's normal login environment when you start it.

## 8. Prove the change

1. Create an issue for a small README change in a selected repository.
2. Comment `/start` as your personal GitHub account.
3. Wait for the dedicated account's engine to merge the pull request.
4. Check the merged change.
5. Confirm that your personal GitHub account merged it.
6. Confirm that the App bot approved it.
7. Confirm that the original timer remained inactive.

Do not merge the test pull request manually.
A manual merge does not prove that the engine works.

If you must return to the original account, stop the dedicated timer first.
Wait until its service is inactive.
Start the original timer only after those checks pass.

## 9. Add a repository

Do these steps as your personal account.
You do not need the dedicated account.

1. Make sure that the token includes the repository.
2. Make sure that the GitHub App is installed on the repository.
3. Run `ghafk init` in your own clone.
4. Commit `.ghafk/WORKFLOW.md` and push it to the default branch.

The engine finds the repository on its next tick and clones it.
Run `ghafk status` to see the repository.

## 10. Update ghafk

With the automatic setup, run `ghafk update`, then `ghafk engine setup`.
`ghafk update` alone no longer updates the engine. Better: move to
`ghafk start` (see the note at the top of this page).
With a manual setup, run these commands from your personal account:

```sh
sudo -iu ghafk go install github.com/mike-diff/ghafk@latest
sudo -iu ghafk ghafk status
```

The next tick uses the new version.
You do not need to start the timer again.
