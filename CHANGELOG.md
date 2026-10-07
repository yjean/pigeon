# Changelog

All notable changes to pigeon. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and versions follow [Semantic Versioning](https://semver.org/) (pre-1.0: minor versions may break things).

## [Unreleased]

## [0.2.0] - 2026-10-07

### Added
- Per-account signatures: `~/.config/pigeon/signatures/<email>.txt`, inserted under what you
  write and above the quote in replies and forwards.
- 📎 in front of list rows (mailboxes and search results) for conversations with attachments.
- `q` in the list asks before quitting (`q` or `y` confirms, any other key stays).

### Changed
- Conversations read newest message first, like the thread list, and open at the top.

### Fixed
- Accents garbled (`Ã©` for `é`) in emails declaring a charset other than UTF-8, such as
  Windows-1252 from Outlook: Gmail already converts them to UTF-8.

## [0.1.0] - 2026-10-06

First usable version.

### Added
- Several Gmail accounts (OAuth 2.0 + PKCE, tokens in the macOS Keychain), switched from a rail.
- Thread list and reading pane, with a disk cache for instant startup and incremental sync.
- Near-instant sync through Gmail history, polled every 10 s and on terminal focus.
- Compose, reply, reply all and forward (in-app editor or `$EDITOR`), drafts.
- Address autocomplete in To/Cc, learned from sent mail and synced senders.
- Attach files (`ctrl+a` or drag & drop); forwards keep their attachments.
- Links & attachments picker (`o`): open, save, copy.
- Archive, trash, star, mark unread, with undo.
- Unread-only inbox by default (`U` toggles), search with Gmail syntax (`/`).
- Catppuccin Mocha theme.
- `make install`.

[Unreleased]: https://github.com/yjean/pigeon/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/yjean/pigeon/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/yjean/pigeon/releases/tag/v0.1.0
