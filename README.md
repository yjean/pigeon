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
- [x] TUI: account rail, thread list, reading pane, status bar
- [x] Disk cache: instant startup, incremental sync (by thread historyId), prefetch
- [x] Compose / reply / reply all / forward (in-app editor or `$EDITOR`), save drafts
- [x] Archive / trash / star / mark unread, with undo
- [ ] Labels sidebar
- [ ] Search
- [ ] Push-style sync (Gmail history API) instead of 60 s polling

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

## Keybindings

| Keys | Action |
|---|---|
| `j` / `k`, `↓` / `↑` | next / previous conversation (scroll when reading) |
| `J` / `K` | next / previous conversation, from anywhere |
| `enter` `l` `tab` | read the conversation (marks it read) |
| `esc` `h` `q` | back to the list |
| `gg` / `G` | top / bottom |
| `space`, `ctrl+d` / `ctrl+u` | scroll the conversation |
| `1`–`9`, `[` / `]` | switch account |
| `gi` `gs` `gt` `gd` `ga` | inbox, starred, sent, drafts, all mail |
| `e` / `#` | archive / move to trash |
| `s` / `u` | star / mark unread (toggles) |
| `z` | undo the last archive / trash |
| `c` | compose a new message |
| `r` / `a` / `f` | reply / reply all / forward the open conversation |
| `ctrl+enter` / `ctrl+s` | send (in compose; ctrl+enter needs Ghostty, kitty, WezTerm…) |
| `ctrl+e` | edit the body in `$EDITOR` (in compose) |
| `tab` / `shift+tab` | next / previous field (in compose) |
| `esc` | close compose: save draft, discard, or keep editing |
| `U` | toggle unread only / all conversations (the inbox starts unread-only) |
| `ctrl+r` | sync now (also every 60 s); drops conversations read meanwhile from the unread view |
| `?` | help |

## Design notes

- **Theme**: Catppuccin Mocha (`internal/tui/theme.go`), colors mapped to the Catppuccin
  style guide roles; modes match LazyVim's lualine (NORMAL blue, INSERT green, READ mauve).
  No background is painted except selection and badges, so transparent terminals stay transparent.

- **Go**: single static binary, ~10 ms startup, and Bubble Tea for the upcoming TUI.
- **Scope** `gmail.modify`: read, compose, send, label, archive, trash (no permanent delete).
- **Secrets**: refresh tokens live in the OS keychain (service `pigeon`), never on disk.
  Refreshed access tokens are written back automatically (`auth.Client`).
- **Config**: `~/.config/pigeon/` (`$XDG_CONFIG_HOME` respected)
  - `credentials.json`: OAuth client (0600)
  - `accounts.json`: ordered account list (= rail order)
- **Cache**: `~/.cache/pigeon/<email>/` (`$XDG_CACHE_HOME` respected), files 0600.
  First paint comes from disk (~1 ms); a sync re-downloads only threads whose `historyId` changed.
- Gmail REST is called directly over `net/http` (no heavy generated SDK), so the binary stays small.
