//go:build !windows

package codex

import keyring "github.com/zalando/go-keyring"

func readCodexKeyring(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func deleteCodexKeyring(service, account string) error {
	return keyring.Delete(service, account)
}
