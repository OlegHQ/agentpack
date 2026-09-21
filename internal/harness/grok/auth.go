package grok

import (
	"fmt"
	"os"
	"path/filepath"

	base "github.com/OlegHQ/agentpack/internal/harness"
	"github.com/OlegHQ/agentpack/internal/paths"
)

var credentialFiles = []string{"auth.json", "mcp_credentials.json"}

func loadSharedCredentials(home string) error {
	shared, err := paths.SharedGrokCredentialsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(shared, 0o700); err != nil {
		return err
	}
	for _, name := range credentialFiles {
		global := filepath.Join(shared, name)
		local := filepath.Join(home, name)
		marker := global + ".initialized"
		globalInfo, globalErr := os.Stat(global)
		if globalErr != nil && !os.IsNotExist(globalErr) {
			return globalErr
		}
		_, markerErr := os.Stat(marker)
		if markerErr != nil && !os.IsNotExist(markerErr) {
			return markerErr
		}
		if os.IsNotExist(globalErr) && os.IsNotExist(markerErr) {
			candidate, err := newestCredential(name, home)
			if err != nil {
				return err
			}
			if candidate != "" {
				if err := copyCredential(candidate, global); err != nil {
					return err
				}
				globalInfo, globalErr = os.Stat(global)
				if globalErr != nil {
					return globalErr
				}
			}
		}
		if globalErr == nil {
			localInfo, err := os.Stat(local)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if err == nil && localInfo.Mode().IsRegular() && localInfo.ModTime().After(globalInfo.ModTime()) {
				if err := copyCredential(local, global); err != nil {
					return err
				}
			}
			if err := copyCredential(global, local); err != nil {
				return err
			}
		} else if !os.IsNotExist(markerErr) {
			if err := os.Remove(local); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func persistCredentials(ctx base.LaunchContext) error {
	home, err := paths.StagingGrokHomeDirForMode(ctx.ProjectRoot, ctx.Mode.Name())
	if err != nil {
		return err
	}
	shared, err := paths.SharedGrokCredentialsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(shared, 0o700); err != nil {
		return err
	}
	for _, name := range credentialFiles {
		local, global := filepath.Join(home, name), filepath.Join(shared, name)
		localInfo, err := os.Stat(local)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		globalInfo, globalErr := os.Stat(global)
		if globalErr != nil && !os.IsNotExist(globalErr) {
			return globalErr
		}
		if err == nil && localInfo.Mode().IsRegular() {
			if os.IsNotExist(globalErr) || localInfo.ModTime().After(globalInfo.ModTime()) {
				if err := copyCredential(local, global); err != nil {
					return err
				}
			}
		} else if os.IsNotExist(err) {
			if err := os.Remove(global); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err := os.WriteFile(global+".initialized", nil, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func newestCredential(name, home string) (string, error) {
	var latest string
	var latestInfo os.FileInfo
	consider := func(path string) error {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() && (latestInfo == nil || info.ModTime().After(latestInfo.ModTime())) {
			latest, latestInfo = path, info
		}
		return nil
	}
	if err := consider(filepath.Join(home, name)); err != nil {
		return "", err
	}
	if native, ok := nativeHome(); ok {
		if err := consider(filepath.Join(native, name)); err != nil {
			return "", err
		}
	}
	root, err := paths.UserAgentpackHome()
	if err != nil {
		return "", err
	}
	projects, err := os.ReadDir(filepath.Join(root, "projects"))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	for _, project := range projects {
		if project.IsDir() {
			if err := consider(filepath.Join(root, "projects", project.Name(), "grok-home", name)); err != nil {
				return "", err
			}
		}
	}
	return latest, nil
}

func copyCredential(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("credential source is not a file: %s", source)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".credential-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chtimes(temporary.Name(), info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), destination)
}
