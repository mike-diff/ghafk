# Run as a GitHub App

By default, ghafk uses your `gh` login on GitHub. With a GitHub App, its
comments, labels and reviews show as `<app>[bot]`.

## Set up the app

1. Create a GitHub App under your account or your organization.
2. Turn off the webhook.
3. Give the app these repository permissions:

   | Permission | Access |
   |---|---|
   | Contents | Read-only |
   | Issues | Read and write |
   | Pull requests | Read and write |
   | Metadata | Read-only (the default) |

4. Generate a private key.
5. Install the app on the repositories that ghafk works. "All repositories"
   also includes each repository that you add later.
6. Save the App ID in `~/.ghafk/app`.
7. Save the key at `~/.ghafk/app.pem`. Only you must be able to read it
   (mode `0600` or `0400`).

## What changes

- For each repository, ghafk gets a short-lived token. The token can only
  access that repository, with the permissions above.
- ghafk still opens and merges pull requests with your login. Thus the
  work counts on your GitHub profile, and the app can review it.

> [!NOTE]
> If you do not configure the app, ghafk uses your login. If ghafk cannot
> get a token, it writes one line to the log and uses your login.
