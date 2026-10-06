// pigeon — a fast, keyboard-driven Gmail client for the terminal.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/yoann/pigeon/internal/auth"
	"github.com/yoann/pigeon/internal/config"
)

const version = "0.1.0-dev"

const usage = `pigeon 🐦 — Gmail in your terminal

Usage:
  pigeon                          launch the TUI (coming soon)
  pigeon setup <client.json>      install your Google OAuth "Desktop app" client
  pigeon account add [email]      authorize a Gmail account in the browser
  pigeon account list [--check]   list accounts (--check verifies each token live)
  pigeon account remove <email>   revoke and forget an account
  pigeon version

Run 'pigeon setup' first; see README.md for creating the OAuth client.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "pigeon:", err)
		if errors.Is(err, auth.ErrNoCredentials) {
			fmt.Fprintln(os.Stderr, "\nRun `pigeon setup <client.json>` first (see README.md).")
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return tui()
	}
	switch args[0] {
	case "setup":
		return cmdSetup(args[1:])
	case "account", "accounts", "acc":
		return cmdAccount(ctx, args[1:])
	case "version", "--version", "-v":
		fmt.Println("pigeon", version)
		return nil
	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func tui() error {
	accounts, err := config.LoadAccounts()
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		fmt.Print("No accounts yet. Add one with `pigeon account add`.\n\n", usage)
		return nil
	}
	fmt.Println("The TUI is not built yet — next iteration. Accounts ready:")
	for _, a := range accounts {
		fmt.Printf("  [%s] %s\n", a.Initials(), a.Email)
	}
	return nil
}

func cmdSetup(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: pigeon setup <path/to/client_secret.json>")
	}
	dst, err := auth.InstallCredentials(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("✓ OAuth client installed at %s\nNext: pigeon account add\n", dst)
	return nil
}

func cmdAccount(ctx context.Context, args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "add":
		hint := ""
		if len(args) > 1 {
			hint = args[1]
		}
		return accountAdd(ctx, hint)
	case "list", "ls":
		fs := flag.NewFlagSet("account list", flag.ContinueOnError)
		check := fs.Bool("check", false, "verify each account's token against Gmail")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return accountList(ctx, *check)
	case "remove", "rm":
		if len(args) != 2 {
			return errors.New("usage: pigeon account remove <email>")
		}
		return accountRemove(ctx, args[1])
	default:
		return fmt.Errorf("unknown account command %q (add, list, remove)", args[0])
	}
}

func accountAdd(ctx context.Context, hint string) error {
	email, tok, err := auth.Login(ctx, hint, func(f string, a ...any) { fmt.Fprintf(os.Stderr, f, a...) })
	if err != nil {
		return err
	}
	if err := auth.SaveToken(email, tok); err != nil {
		return fmt.Errorf("store token in keychain: %w", err)
	}
	added, err := config.UpsertAccount(config.Account{Email: email, AddedAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	if added {
		fmt.Printf("✓ Added %s\n", email)
	} else {
		fmt.Printf("✓ Re-authorized %s\n", email)
	}
	return nil
}

func accountList(ctx context.Context, check bool) error {
	accounts, err := config.LoadAccounts()
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		fmt.Println("No accounts. Add one with `pigeon account add`.")
		return nil
	}
	status := make([]string, len(accounts))
	if check {
		var wg sync.WaitGroup
		for i, a := range accounts {
			wg.Go(func() { status[i] = checkAccount(ctx, a.Email) })
		}
		wg.Wait()
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for i, a := range accounts {
		fmt.Fprintf(w, "%d\t[%s]\t%s\t%s\t%s\n", i+1, a.Initials(), a.Email, a.AddedAt.Local().Format("2006-01-02"), status[i])
	}
	return w.Flush()
}

func checkAccount(ctx context.Context, email string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c, err := auth.Client(ctx, email)
	if err != nil {
		return "✗ " + err.Error()
	}
	got, err := auth.ProfileEmail(ctx, c)
	if err != nil {
		return "✗ " + err.Error()
	}
	if got != email {
		return "✗ token belongs to " + got
	}
	return "✓ ok"
}

func accountRemove(ctx context.Context, email string) error {
	if err := auth.DeleteToken(ctx, email); err != nil {
		return fmt.Errorf("remove token from keychain: %w", err)
	}
	found, err := config.RemoveAccount(email)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no account %s", email)
	}
	fmt.Printf("✓ Removed %s (token revoked)\n", email)
	return nil
}
