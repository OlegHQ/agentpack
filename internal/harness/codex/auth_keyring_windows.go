package codex

import (
	"syscall"

	"github.com/danieljoos/wincred"
	keyring "github.com/zalando/go-keyring"
)

func readCodexKeyring(service, account string) (string, error) {
	credential, err := wincred.GetGenericCredential(codexKeyringTarget(service, account))
	if err == nil {
		defer clear(credential.CredentialBlob)
		return decodeCodexWindowsPassword(credential.CredentialBlob)
	}
	if err != syscall.ERROR_NOT_FOUND {
		return "", err
	}
	return keyring.Get(service, account)
}

func deleteCodexKeyring(service, account string) error {
	credential, err := wincred.GetGenericCredential(codexKeyringTarget(service, account))
	if err == nil {
		err = credential.Delete()
	}
	if err != nil && err != syscall.ERROR_NOT_FOUND {
		return err
	}
	if err := keyring.Delete(service, account); err != nil && err != keyring.ErrNotFound {
		return err
	}
	return nil
}
