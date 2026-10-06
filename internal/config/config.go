// Package config owns pigeon's on-disk state: the OAuth client credentials
// and the ordered list of accounts (the "workspaces" of the sidebar).
// Secrets (OAuth tokens) are NOT stored here — they live in the OS keychain.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const appName = "pigeon"

// Dir returns the config directory: $XDG_CONFIG_HOME/pigeon or ~/.config/pigeon.
func Dir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, appName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", appName), nil
}

func path(name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// CredentialsPath is where the Google OAuth client JSON is stored.
func CredentialsPath() (string, error) { return path("credentials.json") }

func accountsPath() (string, error) { return path("accounts.json") }

// Account is one Gmail account. Order in the store is the sidebar order.
type Account struct {
	Email   string    `json:"email"`
	AddedAt time.Time `json:"added_at"`
}

// Initials gives the 2-letter badge shown in the account rail (like slk's workspace rail).
func (a Account) Initials() string {
	local, _, _ := strings.Cut(a.Email, "@")
	parts := strings.FieldsFunc(local, func(r rune) bool { return r == '.' || r == '_' || r == '-' || r == '+' })
	switch {
	case len(parts) >= 2:
		return strings.ToUpper(parts[0][:1] + parts[1][:1])
	case len(local) >= 2:
		return strings.ToUpper(local[:2])
	default:
		return strings.ToUpper(local)
	}
}

type accountsFile struct {
	Accounts []Account `json:"accounts"`
}

// LoadAccounts returns the configured accounts (empty if none yet).
func LoadAccounts() ([]Account, error) {
	p, err := accountsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f accountsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return f.Accounts, nil
}

// SaveAccounts atomically writes the account list.
func SaveAccounts(accounts []Account) error {
	p, err := accountsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(accountsFile{Accounts: accounts}, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(p, data, 0o600)
}

// UpsertAccount adds the account, or keeps its position if it already exists.
func UpsertAccount(a Account) (added bool, err error) {
	accounts, err := LoadAccounts()
	if err != nil {
		return false, err
	}
	if slices.ContainsFunc(accounts, func(x Account) bool { return strings.EqualFold(x.Email, a.Email) }) {
		return false, nil
	}
	return true, SaveAccounts(append(accounts, a))
}

// RemoveAccount deletes the account from the list. Reports whether it existed.
func RemoveAccount(email string) (bool, error) {
	accounts, err := LoadAccounts()
	if err != nil {
		return false, err
	}
	n := len(accounts)
	accounts = slices.DeleteFunc(accounts, func(x Account) bool { return strings.EqualFold(x.Email, email) })
	if len(accounts) == n {
		return false, nil
	}
	return true, SaveAccounts(accounts)
}

// WriteFileAtomic writes via a temp file + rename so a crash never leaves a half-written file.
func WriteFileAtomic(p string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}
