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
- [x] Signatures, one per account (plain text, see Config below)
- [x] Attachments: attach files (`ctrl+a` browser or drag & drop), forward with attachments; 📎 on list rows
- [x] Archive / trash / star / mark unread, with undo
- [ ] Labels sidebar
- [x] Search (`/`, Gmail search syntax)
- [x] Links & attachments picker (`o`): open links, save/open attachments, copy links
- [x] Address autocomplete in To/Cc, learned from Sent mail and synced senders
- [x] Near-instant sync: Gmail history polled every 10 s (≈2 quota units), plus on terminal focus

## Build

```sh
make install      # tests, then builds to ~/.local/bin/pigeon (PREFIX=/usr/local to change)
make uninstall    # removes the binary; config, Keychain tokens and cache are kept
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
| `↓` `↑` / `ctrl+n` `ctrl+p`, `tab` | choose / accept an address suggestion (To, Cc) |
| `ctrl+a` | attach a file (in compose); dropping files on the terminal also attaches them |
| `esc` | close compose: save draft, discard, or keep editing |
| `/` | search with Gmail syntax (`from:` `subject:` `has:attachment` `after:2026/09/01` …); `esc` leaves the results |
| `o` | links & attachments of the conversation: `enter` open, `s` save to ~/Downloads (or `$PIGEON_DOWNLOAD_DIR`), `y` copy link, `1`–`9` pick |
| `U` | toggle unread only / all conversations (the inbox starts unread-only) |
| `ctrl+r` | sync now (changes also arrive within ~10 s); drops conversations read meanwhile from the unread view |
| `?` | help |
| `q` `q` / `ctrl+c` | quit (`q` asks first: `q` or `y` confirms, any other key stays) |

## Design notes

- **Attachment clip**: the 📎 in front of a list row is guessed from each message's top-level
  MIME type (`multipart/mixed`), the only hint in Gmail's cheap `metadata` format; a rare
  message without a real file (some calendar invites) can show it too.

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
  - `signatures/<email>.txt`: the account's signature, inserted verbatim under what you
    write (above the quote in replies and forwards). Start it with `-- ` (dash dash space)
    so mail clients recognize it. No file, no signature.
- **Contacts** for autocomplete: `~/.cache/pigeon/<email>/contacts.json`, built from the
  recipients of your last 500 sent emails (then incrementally) and the senders of synced mail.
  No Contacts API scope needed.
- **Cache**: `~/.cache/pigeon/<email>/` (`$XDG_CACHE_HOME` respected), files 0600.
  First paint comes from disk (~1 ms); a sync re-downloads only threads whose `historyId` changed.
- Gmail REST is called directly over `net/http` (no heavy generated SDK), so the binary stays small.
