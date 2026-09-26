# How to write an issue

The issue is the prompt. A clear issue gives a small, correct pull request.
A vague issue gives a question, a wrong change or a park.

## Rules

1. **Ask for one change.** If an issue asks for two changes, write two
   issues.
2. **Say what the change is, not how to make it.** ghafk reads the code and
   finds the files.
3. **Say how to check the result.** Give a command and its expected output
   if you can. The groomer copies it into the acceptance criteria, and the
   judge runs it.
4. **Make the product decisions yourself.** If there is more than one
   correct result, choose one in the issue. Otherwise ghafk asks you.
5. **Keep the issue small.** An issue that changes a few files in one area
   is the best size. Divide a large feature into a sequence of issues.

## Example

A good issue:

> **Add a `--shout` flag**
>
> With `--shout`, the program prints the greeting in uppercase. Without it,
> the output does not change.
>
> Check: `go run . --shout Ada` prints `HELLO, ADA!`.

A bad issue:

> **Improve the output**
>
> The output could be nicer. Maybe add colors or a flag.

The bad issue gives no result to check and leaves the product decision
open. ghafk parks it with a question.

## When ghafk asks a question

If the issue leaves a product decision open, the groomer asks one question
with numbered options. It marks one option as recommended. Reply with a
comment that starts with `/answer`:

```
/answer 2, and keep the old flag as an alias
```

ghafk then writes the contract on the next tick.

## Issues from other people

If a person without write access opens the issue, ghafk does not start it.
A maintainer must read the issue and comment `/start`. If the title or the
description changes after `/start`, the issue parks again.
