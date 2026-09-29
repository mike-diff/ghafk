# Security model

ghafk is automation that uses your credentials. Read this page before you
register a repository.

> [!CAUTION]
> Register only repositories where you trust everyone who can label
> issues.

ghafk also works each repository that you own where `.ghafk/WORKFLOW.md`
is on the default branch. A person with write access can add that file.
See [repositories](configuration.md#repositories).

## Agents run in an OS sandbox

The worker, the groomer, the judge, the reconciler and the checks run
inside an operating-system sandbox, as you. ghafk makes each run a new
sandbox that ends with the run:

- On Linux, [bubblewrap](https://github.com/containers/bubblewrap) with
  new user, PID, IPC, UTS, cgroup and network namespaces. On macOS,
  `sandbox-exec` with a deny-by-default profile.
- The sandbox sees the worktree read-write, the repository's `.git`
  read-only, a fresh home directory that is deleted after the run, a
  per-repository cache directory, the harness's login file, and the
  program directories the run needs. When a program is a pyenv or rbenv
  shim, the version manager's folder (for example `~/.pyenv`) is
  readable too, because the shim runs the manager. System directories
  are read-only.
  The Go toolchains in your module download cache
  (`golang.org/toolchain`) are readable too, so a repository that needs
  a newer Go builds without a download; other cached modules stay
  hidden.
  The run can read everything in the repository's `.git`: local
  branches, stashes and `.git/config`. Do not keep a token in a remote
  URL.
- Everything else in your home is invisible: `~/.ssh`, `~/.config/gh`,
  other repositories, browser and cloud logins, shell history. The
  session bus, the SSH agent socket and `/run/user` are not visible. The
  environment is cleared and set explicitly.
- The sandbox has no direct network. An HTTP CONNECT proxy outside the
  sandbox carries the traffic of the harness, `go`, `npm`, `pnpm` and
  `curl`. The proxy resolves each allowed host itself and refuses
  loopback, private, link-local, carrier-grade NAT and multicast
  addresses, so an `egress:` name cannot reach the usual addresses of
  services on your machine or LAN. It does not know every address of
  your network: a public IPv6 address of a LAN device, or a public
  address of this machine, stays reachable if an allowed name resolves
  to it. The proxy allows a fixed list of model API hosts, npm, the Go
  module proxy and checksum database, PyPI and models.dev. A workflow
  `egress:` line adds hosts for one repository, and an `egress:` line in
  `~/.ghafk/config` adds hosts for every run. Denied hosts appear in the
  park comment, and in the tick log when the run still succeeds. A
  service on this machine, such as a local model server on port 11434,
  is not reachable unless a `local:` line in `~/.ghafk/config` names its
  port. Then a run reaches that one port on `localhost` and nothing else
  on this machine. Only name a port whose service you trust with agent
  requests; the agent can call any endpoint that service has.
- The GitHub token never enters a run. Each harness uses its own login:
  Claude Code, Codex, pi, sesh and opencode get a copy of their real
  login file in the run home. When a run refreshed the login, ghafk copies back only the
  token fields that a refresh changes (access and refresh tokens and
  their expiry). A harness API key in your environment, for example
  `OPENAI_API_KEY` or `ANTHROPIC_API_KEY`, also counts as a login. ghafk
  refuses omp, because its model client ignores the proxy. Claude Code with
  a subscription on macOS keeps its login in the Keychain, which the
  sandbox blocks: run `claude setup-token` once and put
  `CLAUDE_CODE_OAUTH_TOKEN=<token>` in `~/.ghafk/env` (`chmod 600`).
  Harness provider configuration (`~/.codex/config.toml`,
  `~/.pi/agent/models.json`, `~/.config/opencode`, `~/.sesh/providers.json`)
  is copied into the run home the same way. Harness API keys pass to a
  run only for the harness they belong to, plus names you list in an
  `env:` line in `~/.ghafk/config`; nothing else from your environment is
  passed by name. The login copy-back reads only a regular file of the
  expected size inside the run home, after every run. It never adds a
  provider, changes a credential type or keeps a value that a harness
  could run as a command or expand, such as a value that starts with
  `!` or contains `$`. A malicious run can still replace the tokens of
  its own harness with other tokens of the same shape.
- Harness sandboxes are off inside ghafk's sandbox, because they cannot
  nest: Codex runs with `--sandbox danger-full-access`, Claude Code with
  its sandbox setting disabled.
- Before each tick, ghafk checks that the sandbox starts. Before each
  run, it checks that the proxy answers and that the harness and checks
  programs resolve. It refuses a program inside a repository that it
  works, so a repository cannot choose what runs outside the sandbox.
  If a check fails, the run parks with the reason and the fix. ghafk
  never runs an agent outside the sandbox.
- A run has no controlling terminal and cannot create new user
  namespaces. On Linux, if your home is a symbolic link, ghafk also
  hides the directory it points to.
- ghafk's own `git add` and `git diff` outside the sandbox still apply
  the filters that your global git configuration defines, for example
  Git LFS, to files that a run changed. A `.gitattributes` file in the
  repository can select those filters. Do not define a filter that runs
  a script from the working tree.

An agent can still read its own model login during a run and reach the
allowed hosts, and some allowed hosts can carry data out. That is the
trade for reusing the harnesses and logins you already have.

Further accepted limits:

- On Linux, `/var/tmp`, `/mnt` and `/opt` stay readable inside the
  sandbox, like the rest of the system directories.
- A harness installed by pnpm's own installer gets its whole global
  package directory bound read-only, so the run can also read the other
  packages installed there.
- The macOS profile lets a run read global preferences through
  `cfprefsd` (`kCFPreferencesAnyApplication`), which exposes settings
  such as locale and region, and read `~/.CFUserTextEncoding`.
- On macOS, the proxy on its loopback port has no authentication: any
  process of your user can use it, with the same allowlist, while a run
  is in progress. macOS has no process namespaces, so each run starts in
  its own session, and ghafk ends each run by killing the run's process
  group and sweeping the process list for the run's marker and session;
  a process that escapes both can no longer reach anything of the next
  run, because on macOS every run also gets a fresh cache directory. Each
  run works in a fresh, unguessable work subdirectory.

## Secrets in the output

An agent can see some secrets: its own model login, secrets that the
repository already holds, and variables that you list in `env:`. ghafk
checks what leaves your machine for these values:

- ghafk knows the values that it passed into the run, your GitHub token,
  its App key and the values in `~/.ghafk/env`. It also looks for common
  key formats: Anthropic, OpenAI, GitHub, AWS, Slack and Google keys, and
  private key blocks.
- Before each push, ghafk folds the agent's commits into one commit and
  checks the change: the added lines, the file names and the commit
  message. If it finds a value, it does not push. It tells the worker the
  file and the line, never the value, and asks the worker to remove it.
  It checks again, and parks only if the worker does not remove the value
  after two requests. A branch that holds a value is never merged.
- A key format that the base branch already holds does not count, so a
  repository with an old committed secret keeps working. A value that
  ghafk passed into the run always counts.
- For a test fixture that looks like a key, add its path to a
  `secrets-allow:` line in `.ghafk/WORKFLOW.md`. The allowlist never
  covers a value that ghafk passed into the run. An agent cannot add to
  it, because ghafk reads the file from the default branch and never
  merges a change to `.ghafk/`.
- In comments, reviews, cards and pull request text, ghafk replaces such
  a value with a placeholder and continues.

ghafk does not catch a value that it does not know and that has no known
format, or a value that an agent hides on purpose, for example split
across lines or encoded other than in base64 or hex. GitHub push
protection is a second layer only on repositories where GitHub secret
protection is on: public repositories, and private repositories with a
paid plan. It never covers comments. ghafk checks before anything
reaches GitHub. A commit that ghafk did not push stays in the local
clone until git removes it.

## The engine account (deprecated)

Earlier releases ran the whole engine as a separate system account with
`ghafk engine setup`. That account's agents ran without a sandbox and
could read its GitHub token and App key. `ghafk start` now refuses while
an engine exists; run `ghafk engine remove --purge` to migrate. The
`ghafk engine remove` command stays for one release.

## Issue text is a prompt

A person who can put text in front of an agent can try to give it
instructions. ghafk applies these limits:

- Only comments from people with write access go to the agents.
- An issue from a person without write access waits for `/start` from a
  maintainer. If its title or description changes after `/start`, the
  issue parks. It then needs a new `/start`.
- When the title or description of any issue changes after ghafk wrote
  its contract, ghafk writes a new contract from the new text before it
  starts the work, so a stale contract is never built.
- Slash commands count only from people with write access.
- ghafk works and merges only the pull requests that it opened, from the
  same repository.
- A change to `.github/` or `.ghafk/` always waits for you. ghafk never pushes a change to `.github/`, because GitHub Actions runs a pushed workflow with the secrets of the repository. It puts the change in the park comment instead.
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
  hidden markers in that output. See [secrets in the output](#secrets-in-the-output).
- **There is no spending limit.** Each issue costs some agent runs. A
  failure parks the issue and does not retry, so nothing loops. Monitor
  your usage.

## Report a vulnerability

See [SECURITY.md](../SECURITY.md).
