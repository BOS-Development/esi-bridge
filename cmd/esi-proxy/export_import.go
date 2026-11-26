package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"golang.org/x/term"

	"github.com/BOS-Development/esi-proxy/internal/config"
	"github.com/BOS-Development/esi-proxy/internal/cryptox"
	"github.com/BOS-Development/esi-proxy/internal/db"
	"github.com/BOS-Development/esi-proxy/internal/store"
)

// saltSize must match cryptox.NewSalt's output length.
const saltSize = 16

// exportedCharacter is a character's plaintext state as carried inside an
// export bundle. The bundle file itself is encrypted with a
// passphrase-derived key (see cryptox.DeriveKey), so this plaintext never
// touches disk unencrypted.
type exportedCharacter struct {
	ID                   int64    `json:"id"`
	AccountName          string   `json:"account_name"`
	Name                 string   `json:"name"`
	RefreshToken         string   `json:"refresh_token"`
	AccessToken          string   `json:"access_token"`
	AccessTokenExpiresAt string   `json:"access_token_expires_at"`
	Scopes               []string `json:"scopes"`
	// ClientID is the EVE SSO client_id the tokens belong to ("" if unknown,
	// including bundles from older versions).
	ClientID string `json:"client_id,omitempty"`
}

type bundle struct {
	Characters []exportedCharacter `json:"characters"`
}

func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	out := fs.String("out", "", "path to write the encrypted bundle to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("--out is required")
	}

	dataDir, err := config.Dir()
	if err != nil {
		return fmt.Errorf("resolve config dir: %w", err)
	}
	conn, err := db.Open(dataDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer conn.Close()

	masterKey, err := cryptox.MasterKey(dataDir)
	if err != nil {
		return fmt.Errorf("load master key: %w", err)
	}
	st := store.New(conn)

	chars, err := st.ListCharacters()
	if err != nil {
		return fmt.Errorf("list characters: %w", err)
	}
	if len(chars) == 0 {
		return errors.New("no characters to export")
	}

	accountNames := make(map[int64]string)
	var b bundle
	for _, c := range chars {
		accountName, ok := accountNames[c.AccountID]
		if !ok {
			acc, err := st.GetAccount(c.AccountID)
			if err != nil {
				return fmt.Errorf("get account for character %d: %w", c.ID, err)
			}
			accountName = acc.Name
			accountNames[c.AccountID] = accountName
		}

		refresh, err := cryptox.Open(masterKey, c.RefreshTokenEnc)
		if err != nil {
			return fmt.Errorf("decrypt refresh token for %s: %w", c.Name, err)
		}
		access, err := cryptox.Open(masterKey, c.AccessTokenEnc)
		if err != nil {
			return fmt.Errorf("decrypt access token for %s: %w", c.Name, err)
		}

		b.Characters = append(b.Characters, exportedCharacter{
			ID:                   c.ID,
			AccountName:          accountName,
			Name:                 c.Name,
			RefreshToken:         string(refresh),
			AccessToken:          string(access),
			AccessTokenExpiresAt: store.FormatTime(c.AccessTokenExpiresAt),
			Scopes:               c.Scopes,
			ClientID:             c.ClientID,
		})
	}

	payload, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("marshal bundle: %w", err)
	}

	passphrase, err := readPassphrase("Bundle passphrase (used to protect the exported tokens): ")
	if err != nil {
		return err
	}
	confirm, err := readPassphrase("Confirm passphrase: ")
	if err != nil {
		return err
	}
	if passphrase != confirm {
		return errors.New("passphrases did not match")
	}

	salt, err := cryptox.NewSalt()
	if err != nil {
		return err
	}
	key := cryptox.DeriveKey(passphrase, salt)
	sealed, err := cryptox.Seal(key, payload)
	if err != nil {
		return fmt.Errorf("encrypt bundle: %w", err)
	}

	if err := os.WriteFile(*out, append(salt, sealed...), 0o600); err != nil {
		return fmt.Errorf("write bundle: %w", err)
	}

	fmt.Printf("Exported %d character(s) to %s\n", len(b.Characters), *out)
	return nil
}

func runImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	in := fs.String("in", "", "path to read the encrypted bundle from")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return errors.New("--in is required")
	}

	raw, err := os.ReadFile(*in)
	if err != nil {
		return fmt.Errorf("read bundle: %w", err)
	}
	if len(raw) < saltSize {
		return errors.New("bundle file is too short to be valid")
	}
	salt, sealed := raw[:saltSize], raw[saltSize:]

	passphrase, err := readPassphrase("Bundle passphrase: ")
	if err != nil {
		return err
	}
	key := cryptox.DeriveKey(passphrase, salt)
	payload, err := cryptox.Open(key, sealed)
	if err != nil {
		return errors.New("could not decrypt bundle: wrong passphrase or corrupted file")
	}

	var b bundle
	if err := json.Unmarshal(payload, &b); err != nil {
		return fmt.Errorf("parse bundle: %w", err)
	}

	dataDir, err := config.Dir()
	if err != nil {
		return fmt.Errorf("resolve config dir: %w", err)
	}
	conn, err := db.Open(dataDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer conn.Close()

	masterKey, err := cryptox.MasterKey(dataDir)
	if err != nil {
		return fmt.Errorf("load master key: %w", err)
	}
	st := store.New(conn)

	for _, ec := range b.Characters {
		account, err := st.GetOrCreateAccountByName(ec.AccountName)
		if err != nil {
			return fmt.Errorf("get/create account %q: %w", ec.AccountName, err)
		}

		refreshEnc, err := cryptox.Seal(masterKey, []byte(ec.RefreshToken))
		if err != nil {
			return fmt.Errorf("encrypt refresh token for %s: %w", ec.Name, err)
		}
		accessEnc, err := cryptox.Seal(masterKey, []byte(ec.AccessToken))
		if err != nil {
			return fmt.Errorf("encrypt access token for %s: %w", ec.Name, err)
		}

		expiresAt, err := store.ParseTimeRFC3339(ec.AccessTokenExpiresAt)
		if err != nil {
			return fmt.Errorf("parse expiry for %s: %w", ec.Name, err)
		}

		err = st.UpsertCharacter(store.Character{
			ID:                   ec.ID,
			AccountID:            account.ID,
			Name:                 ec.Name,
			RefreshTokenEnc:      refreshEnc,
			AccessTokenEnc:       accessEnc,
			AccessTokenExpiresAt: expiresAt,
			Scopes:               ec.Scopes,
			ClientID:             ec.ClientID,
		})
		if err != nil {
			return fmt.Errorf("save character %s: %w", ec.Name, err)
		}
	}

	fmt.Printf("Imported %d character(s) from %s\n", len(b.Characters), *in)
	fmt.Println("Stop using these characters on the machine they were exported from: EVE SSO rotates")
	fmt.Println("refresh tokens, so two installs refreshing the same tokens can invalidate each other.")
	return nil
}

func readPassphrase(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read passphrase: %w", err)
	}
	return string(b), nil
}
