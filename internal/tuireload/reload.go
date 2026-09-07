// Package tuireload coordinates cooperative reloads through the task database.
// Old clients simply ignore the request; no process or tmux session is killed.
package tuireload

import (
	"crypto/rand"
	"encoding/hex"
)

const setting = "tui_reload_request"

type Store interface {
	GetSetting(string) (string, error)
	SetSetting(string, string) error
}

func Token(store Store) (string, error) { return store.GetSetting(setting) }

func Request(store Store) error {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	return store.SetSetting(setting, hex.EncodeToString(token[:]))
}
