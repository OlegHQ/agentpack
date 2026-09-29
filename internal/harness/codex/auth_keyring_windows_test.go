package codex

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/danieljoos/wincred"
)

func TestWindowsCodexKeyringInterop(t *testing.T) {
	service := "agentpack-test"
	account := fmt.Sprintf("codex-%d-%d", os.Getpid(), time.Now().UnixNano())
	password := "synthetic-auth-key-✓"
	units := utf16.Encode([]rune(password))
	blob := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(blob[index*2:], unit)
	}
	credential := wincred.NewGenericCredential(codexKeyringTarget(service, account))
	credential.UserName = account
	credential.CredentialBlob = blob
	if err := credential.Write(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = credential.Delete() })
	actual, err := readCodexKeyring(service, account)
	if err != nil || actual != password {
		t.Fatalf("read Codex-style Windows credential = %q, %v", actual, err)
	}
	if err := deleteCodexKeyring(service, account); err != nil {
		t.Fatal(err)
	}
}
