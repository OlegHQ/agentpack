package integration_test

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const installerFixtureVersion = "9.8.7"

func TestReleaseInstallerVersionsMatchCLI(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	repositoryRoot := filepath.Dir(filepath.Dir(source))
	for _, relative := range []string{
		"scripts/agentpack-installer.sh",
		"scripts/agentpack-installer.ps1",
		"integration/cli_test.go",
		"README.md",
	} {
		contents, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(contents), "0.3.14") {
			t.Errorf("%s does not contain release version 0.3.14", relative)
		}
	}
}

func TestReleaseInstallerDownloadsVerifiesAndInstalls(t *testing.T) {
	releaseRoot := t.TempDir()
	archiveName := writeInstallerFixture(t, releaseRoot)
	server := httptest.NewServer(http.FileServer(http.Dir(releaseRoot)))
	defer server.Close()

	installDir := filepath.Join(t.TempDir(), "bin")
	command := installerCommand(t)
	command.Env = append(os.Environ(),
		"AGENTPACK_VERSION="+installerFixtureVersion,
		"AGENTPACK_DOWNLOAD_URL="+server.URL,
		"AGENTPACK_INSTALL_DIR="+installDir,
		"AGENTPACK_NO_MODIFY_PATH=1",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("installer failed: %v\n%s", err, output)
	}
	binaryName := "agentpack"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	contents, err := os.ReadFile(filepath.Join(installDir, binaryName))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != installerFixtureBinary() {
		t.Fatalf("installed contents = %q from %s", contents, archiveName)
	}
}

func TestReleaseInstallerRejectsChecksumMismatch(t *testing.T) {
	releaseRoot := t.TempDir()
	archiveName := writeInstallerFixture(t, releaseRoot)
	if err := os.WriteFile(filepath.Join(releaseRoot, archiveName), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.FileServer(http.Dir(releaseRoot)))
	defer server.Close()

	command := installerCommand(t)
	command.Env = append(os.Environ(),
		"AGENTPACK_VERSION="+installerFixtureVersion,
		"AGENTPACK_DOWNLOAD_URL="+server.URL,
		"AGENTPACK_INSTALL_DIR="+filepath.Join(t.TempDir(), "bin"),
		"AGENTPACK_NO_MODIFY_PATH=1",
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("installer accepted a bad checksum:\n%s", output)
	}
}

func installerCommand(t *testing.T) *exec.Cmd {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	repositoryRoot := filepath.Dir(filepath.Dir(source))
	if runtime.GOOS == "windows" {
		powerShell, err := exec.LookPath("pwsh")
		if err != nil {
			t.Skip("PowerShell Core unavailable")
		}
		return exec.Command(powerShell, "-NoLogo", "-NoProfile", "-File", filepath.Join(repositoryRoot, "scripts", "agentpack-installer.ps1"), "-NoModifyPath")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell unavailable")
	}
	return exec.Command(shell, filepath.Join(repositoryRoot, "scripts", "agentpack-installer.sh"), "--no-modify-path")
}

func writeInstallerFixture(t *testing.T, root string) string {
	t.Helper()
	archiveName := installerArchiveName()
	archivePath := filepath.Join(root, archiveName)
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		writer := zip.NewWriter(file)
		entry, createErr := writer.Create("agentpack.exe")
		if createErr == nil {
			_, createErr = io.WriteString(entry, installerFixtureBinary())
		}
		closeArchiveErr := writer.Close()
		closeFileErr := file.Close()
		if createErr != nil || closeArchiveErr != nil || closeFileErr != nil {
			t.Fatalf("write zip: %v %v %v", createErr, closeArchiveErr, closeFileErr)
		}
	} else {
		gzipWriter := gzip.NewWriter(file)
		tarWriter := tar.NewWriter(gzipWriter)
		body := installerFixtureBinary()
		if err := tarWriter.WriteHeader(&tar.Header{Name: "agentpack", Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		_, writeErr := io.WriteString(tarWriter, body)
		closeTarErr := tarWriter.Close()
		closeGzipErr := gzipWriter.Close()
		closeFileErr := file.Close()
		if writeErr != nil || closeTarErr != nil || closeGzipErr != nil || closeFileErr != nil {
			t.Fatalf("write tar: %v %v %v %v", writeErr, closeTarErr, closeGzipErr, closeFileErr)
		}
	}
	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if err := os.WriteFile(filepath.Join(root, "checksums.txt"), []byte(fmt.Sprintf("%x  %s\n", digest, archiveName)), 0o644); err != nil {
		t.Fatal(err)
	}
	return archiveName
}

func installerArchiveName() string {
	osName := runtime.GOOS
	arch := runtime.GOARCH
	if arch == "x86_64" {
		arch = "amd64"
	}
	if arch == "aarch64" {
		arch = "arm64"
	}
	if osName == "windows" {
		arch = "amd64"
		return fmt.Sprintf("agentpack_%s_windows_%s.zip", installerFixtureVersion, arch)
	}
	return fmt.Sprintf("agentpack_%s_%s_%s.tar.gz", installerFixtureVersion, osName, arch)
}

func installerFixtureBinary() string {
	return "agentpack installer fixture\n"
}
