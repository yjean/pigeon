# pigeon 🐦

A fast, keyboard-driven Gmail client for the terminal, inspired by [slk](https://getslk.sh/).

```
┌──┬──────────────────────────────────────────────┐
│YA│  inbox list (top pane)                       │
│PE│──────────────────────────────────────────────│
│  │  selected email (bottom pane)                │
└──┴──────────────────────────────────────────────┘
 accounts rail (like slk workspaces)
```

## Status

- [x] CLI skeleton
- [x] Add / list / remove Gmail accounts (OAuth 2.0 + PKCE, tokens in macOS Keychain)
- [ ] TUI: account rail, mail list, reading pane
- [ ] Compose / reply / forward
- [ ] Local cache + sync (Gmail history API)

## Build

```sh
go build -o pigeon . && mv pigeon /usr/local/bin/   # or: go install .
```

## One-time setup: your own Google OAuth client

Gmail scopes are "restricted", so pigeon uses an OAuth client **you** own (free, for personal use):

1. https://console.cloud.google.com/ → create a project (e.g. `pigeon`).
2. **APIs & Services → Library** → enable **Gmail API**.
3. **Google Auth Platform → Branding**: fill in the app name and your email.
   **Audience**: *External* (or *Internal* if every account is in your Workspace org),
   then add each Gmail address you will use as a **test user**.
4. **Data Access → Add scopes** → `https://www.googleapis.com/auth/gmail.modify`.
5. **Clients → Create client** → type **Desktop app** → download the JSON.
6. `pigeon setup ~/Downloads/client_secret_XXXX.json`

> ⚠️ With an *External* app in **Testing** status, Google expires refresh tokens after
> 7 days, so you'd have to re-run `pigeon account add`. Use an *Internal* app (Workspace)
> or publish the app (unverified is fine for your own use; you'll click through a warning).

## Usage

```sh
pigeon account add                 # opens the browser, pick the account
pigeon account add me@gmail.com    # pre-select an account
pigeon account list --check        # verify tokens against Gmail
pigeon account remove me@gmail.com # revoke at Google + delete from Keychain
```

## Design notes

- **Go**: single static binary, ~10 ms startup, and Bubble Tea for the upcoming TUI.
- **Scope** `gmail.modify`: read, compose, send, label, archive, trash (no permanent delete).
- **Secrets**: refresh tokens live in the OS keychain (service `pigeon`), never on disk.
  Refreshed access tokens are written back automatically (`auth.Client`).
- **Config**: `~/.config/pigeon/` (`$XDG_CONFIG_HOME` respected)
  - `credentials.json`: OAuth client (0600)
  - `accounts.json`: ordered account list (= rail order)
- Gmail REST is called directly over `net/http` (no heavy generated SDK), so the binary stays small.
