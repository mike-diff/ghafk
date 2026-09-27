# Security model

ghafk is automation that uses your credentials. Read this page before you
register a repository.

> [!CAUTION]
> Register only repositories where you trust everyone who can label
> issues.

ghafk also works each repository that you own where `.ghafk/WORKFLOW.md`
is on the default branch. A person with write access can add that file.
See [repositories](configuration.md#repositories).

## Agents run as you

The worker, the groomer, the judge and the reconciler run with your user
account. They have your network access and your environment. This
includes your `gh` login, SSH keys, cloud credentials, model API keys,
other repositories and `~/.ghafk/app.pem`.

ghafk removes the GitHub token variables from the environment of the
agent. This is not isolation. Only a harness with its own sandbox limits
what the agent can touch, for example `codex --sandbox workspace-write`.
For more isolation, use `ghafk engine setup` or a VM.

## The engine account

`ghafk engine setup` runs the engine as a separate system account:
`ghafk` on Linux, `_ghafk` on macOS. The agents then run as that account,
not as you.

- The account cannot read your home directory if its mode is `0750` or
  `0700`.
- On Linux, the systemd service also hides all home directories, makes the
  system read-only except the engine home, gives the engine a private
  `/tmp` and prevents new privileges. `sudo` and setuid programs do not
  work in the service.
- On macOS, only file permissions protect your files. macOS has no
  equivalent service sandbox.
- The ghafk binary and the service file belong to root. The agents cannot
  change them.
- Setup runs each privileged step as its own `sudo` command. It never runs
  a program or a shell file of the engine account in your terminal.
- Setup writes the files in the engine home as the engine account, never as
  root. A link that an agent puts in the engine home cannot make root read
  or change another file.

These limits stay:

- The agents run as the engine account, so they can read what it can
  read. This includes its GitHub token, the App key and its clones. ghafk
  keeps the token out of the agent environment, but an agent can read the
  file. Give the token access only to the repositories that ghafk works.
- You are in the group of the engine account, so you can read its status,
  log and clones. You cannot read its token or App key.

## Issue text is a prompt

A person who can put text in front of an agent can try to give it
instructions. ghafk applies these limits:

- Only comments from people with write access go to the agents.
- An issue from a person without write access waits for `/start` from a
  maintainer. If its title or description changes after `/start`, the
  issue parks. It then needs a new `/start`.
- Slash commands count only from people with write access.
- ghafk works and merges only the pull requests that it opened, from the
  same repository.
- A change to `.github/` or `.ghafk/` always waits for you.
- An issue or pull request with 100 or more comments parks. GitHub gives
  ghafk only the first 100 comments, so ghafk cannot see newer commands.

## Prompts are not controls

"Do not edit files" and the judge are instructions to a model. They are
not permissions. You can read every [prompt](prompts.md). If you need a real gate, use branch protection and
required reviews on your default branch.

> [!IMPORTANT]
> The approving review of the ghafk GitHub App counts toward a required
> review. ghafk merges as your login, so a rule that requires one approval
> does not stop ghafk. For a human gate, require two approvals or a review
> from code owners.

> [!NOTE]
> ghafk merges immediately after its own checks. If GitHub requires a
> status check that is still running, GitHub refuses the merge and the
> issue parks.

## Git commands of ghafk

An agent can change files in its worktree, including git files. ghafk
does not let those changes run code in its own git commands:

- ghafk uses the git folder that it recorded when it made the worktree.
  It ignores the `.git` file in the worktree.
- ghafk deletes a `config.worktree` file before each git command.
- ghafk runs git without repository hooks, without `core.fsmonitor`,
  without the `ext::` protocol and without the system git config.

Your repository hooks do not run for the commits and pushes of ghafk.

## Other risks

- **Checks run the code of the pull request** on your machine. This
  includes install scripts.
- **Model output is public.** Park comments and pull request descriptions
  include agent output. ghafk disables mentions, closing keywords and
  hidden markers in that output. It does not remove secrets that an agent
  prints.
- **There is no spending limit.** Each issue costs some agent runs. A
  failure parks the issue and does not retry, so nothing loops. Monitor
  your usage.

## Report a vulnerability

See [SECURITY.md](../SECURITY.md).
