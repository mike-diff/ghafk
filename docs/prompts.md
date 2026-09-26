# Prompts

ghafk gives each agent a prompt. This page tells you what each prompt
contains and how to change it.

## Roles

| Role | Task | Answer that ghafk reads |
|---|---|---|
| Groomer | Turns the issue into a contract, or asks one question. | `contract:` or `question:` |
| Worker | Makes the change in a worktree. Also does the repairs. | Two lines of summary |
| Judge | Compares the diff with the contract. | `approve:` or `reject:`, then one line for each acceptance item |
| Reconciler | After a merge, checks the contract of another open issue. | `valid:`, `stale:` or `done:` |

## What a prompt contains

ghafk builds each prompt in this order:

1. The built-in prompt of the role, from the [prompts](../prompts) folder.
2. The shared rules in [common.md](../prompts/common.md). They tell the
   agent to treat repository text as data, and to write in ASD-STE100
   technical English.
3. The text after the settings block in `.ghafk/WORKFLOW.md`. Put rules
   for your repository here, for example "Use the standard library only."
4. The contract, if there is one.
5. The issue and the comments from people with write access. For a
   repair, the failure output also goes here.

The worker prompt includes the `checks` command of the repository. The
worker must run it before it reports done.

## Change a prompt

To replace the built-in prompt of a role, write a file in
`~/.ghafk/prompts/`:

| File | Replaces |
|---|---|
| `groom.md` | The groomer prompt |
| `worker.md` | The worker prompt. `{checks}` becomes the checks command. |
| `judge.md` | The judge prompt |
| `reconcile.md` | The reconciler prompt |
| `common.md` | The shared rules for all roles |

The file applies to all repositories on this machine, from the next run.
To go back to the built-in prompt, delete the file.

> [!WARNING]
> ghafk reads the answer of each agent by its first line. If your prompt
> does not ask for the same answer format as the built-in prompt, ghafk
> cannot read the answer and parks the issue. Start from a copy of the
> built-in file.

> [!NOTE]
> A prompt is an instruction, not a control. To learn what a prompt cannot
> prevent, see the [security model](security.md).
