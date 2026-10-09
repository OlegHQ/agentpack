package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/OlegHQ/agentpack/internal/lockfile"
)

const (
	LauncherNPM     = "npm"
	LauncherPyPI    = "pypi"
	LauncherDocker  = "docker"
	LauncherRemote  = "remote"
	LauncherCommand = "command"
)

const defaultNPMRegistry = "https://registry.npmjs.org"

var (
	npmName     = regexp.MustCompile(`^(@[a-z0-9~-][a-z0-9._~-]*/)?[a-z0-9~-][a-z0-9._~-]*$`)
	npmDistTag  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)
	pypiRequest = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)(\[[^\]]*\])?(==([A-Za-z0-9][A-Za-z0-9.!+_-]*))?$`)
)

// Definition digests what a server definition runs: its type, command,
// arguments, and URL. Environment values are excluded so secrets never
// influence, or leak through, the lockfile.
func (server Server) Definition() string {
	hasher := sha256.New()
	field := func(value string) {
		hasher.Write([]byte(value))
		hasher.Write([]byte{0})
	}
	optional := func(value *string) {
		if value == nil {
			field("")
			return
		}
		field("=" + *value)
	}
	optional(server.Type)
	optional(server.Command)
	optional(server.URL)
	for _, argument := range server.Args {
		field(argument)
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
}

// Describe classifies a server by what launches it and returns the lock record
// that can be written without network access. An npm registry package comes
// back with an empty Status: it still needs ResolveNPM.
func Describe(name string, entry Entry) lockfile.MCPServer {
	server := entry.Server
	record := lockfile.MCPServer{Name: name, Source: string(entry.Source), Definition: server.Definition()}
	if server.IsRemote() {
		record.Launcher, record.Status = LauncherRemote, lockfile.MCPUnpinnable
		if parsed, err := url.Parse(*server.URL); err == nil {
			record.Host = parsed.Host
		}
		record.Reason = "remote server: the host decides what runs"
		return record
	}
	if server.Command == nil {
		record.Launcher, record.Status = LauncherCommand, lockfile.MCPUnpinnable
		record.Reason = "no command or url"
		return record
	}
	program := programName(*server.Command)
	if index, isNPM := npmPackageArgument(program, server.Args); isNPM {
		record.Launcher = LauncherNPM
		if index < 0 {
			record.Status, record.Reason = lockfile.MCPUnpinned, "could not find the package argument"
			return record
		}
		record.Requested = server.Args[index]
		name, spec, ok := splitNPMSpec(record.Requested)
		if !ok {
			record.Status, record.Reason = lockfile.MCPUnpinned, "not an npm registry package spec"
			return record
		}
		record.Package = name
		if _, err := semver.StrictNewVersion(spec); err != nil && !npmDistTag.MatchString(spec) {
			record.Status, record.Reason = lockfile.MCPUnpinned, "version ranges are not resolved; use an exact version or a dist-tag"
		}
		return record
	}
	requested, isPyPI := pypiRequested(program, server.Args)
	switch {
	case isPyPI:
		record.Launcher, record.Status = LauncherPyPI, lockfile.MCPUnpinned
		record.Reason = "PyPI version and hash resolution is not implemented"
		record.Requested = requested
		if match := pypiRequest.FindStringSubmatch(requested); match != nil {
			record.Package, record.Version = match[1], match[4]
		}
	case program == "docker" || program == "podman":
		record.Launcher, record.Status = LauncherDocker, lockfile.MCPUnpinned
		record.Reason = "image digest resolution is not implemented"
		if image := dockerImage(server.Args); image != "" {
			record.Requested = image
			if repository, digest, found := strings.Cut(image, "@"); found && strings.HasPrefix(digest, "sha256:") {
				record.Package, record.Integrity = repository, digest
				record.Status, record.Reason = lockfile.MCPPinned, ""
			}
		}
	default:
		record.Launcher, record.Status = LauncherCommand, lockfile.MCPUnpinnable
		record.Command = *server.Command
		record.Reason = "local command: agentpack does not manage what it runs"
	}
	return record
}

// ResolveNPM asks the registry which exact version a version or dist-tag
// names and returns that version with the registry's tarball integrity.
func ResolveNPM(ctx context.Context, client *http.Client, registry, name, spec string) (version, integrity string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	endpoint := strings.TrimRight(registry, "/") + "/" + strings.ReplaceAll(name, "/", "%2F") + "/" + url.PathEscape(spec)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Accept", "application/json")
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("registry %s is unreachable: %w", registry, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return "", "", fmt.Errorf("registry %s has no %s@%s", registry, name, spec)
	}
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("registry %s answered %s", registry, response.Status)
	}
	var document struct {
		Version string `json:"version"`
		Dist    struct {
			Integrity string `json:"integrity"`
			Shasum    string `json:"shasum"`
		} `json:"dist"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&document); err != nil {
		return "", "", fmt.Errorf("registry %s sent an unreadable answer for %s@%s: %w", registry, name, spec, err)
	}
	if _, err := semver.StrictNewVersion(document.Version); err != nil {
		return "", "", fmt.Errorf("registry %s sent no exact version for %s@%s", registry, name, spec)
	}
	integrity = document.Dist.Integrity
	if integrity == "" {
		if raw, err := hex.DecodeString(document.Dist.Shasum); err == nil && len(raw) != 0 {
			integrity = "sha1-" + base64.StdEncoding.EncodeToString(raw)
		}
	}
	if integrity == "" {
		return "", "", fmt.Errorf("registry %s sent no integrity for %s@%s", registry, name, document.Version)
	}
	return document.Version, integrity, nil
}

// NPMRegistry returns the registry a server's launcher will use as far as
// agentpack can see it: the server's own environment, then the process
// environment, then the public registry. Per-user .npmrc files are not read.
func NPMRegistry(server Server) string {
	for _, key := range []string{"npm_config_registry", "NPM_CONFIG_REGISTRY"} {
		if value := strings.TrimSpace(server.Env[key]); value != "" {
			return strings.TrimRight(value, "/")
		}
	}
	for _, key := range []string{"npm_config_registry", "NPM_CONFIG_REGISTRY"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return strings.TrimRight(value, "/")
		}
	}
	return defaultNPMRegistry
}

// NPMSpec returns the version or dist-tag part of a record's requested spec.
func NPMSpec(record lockfile.MCPServer) string {
	_, spec, _ := splitNPMSpec(record.Requested)
	return spec
}

// ApplyPins rewrites each npm server that the lock pins so it launches the
// exact locked version. A record made for a different definition is ignored:
// the server then runs as written until `agentpack lock` records it again.
func ApplyPins(entries Entries, records []lockfile.MCPServer) Entries {
	pinned := make(Entries, len(entries))
	for name, entry := range entries {
		pinned[name] = entry
		record, found := recordFor(records, name)
		if !found || record.Status != lockfile.MCPPinned || record.Launcher != LauncherNPM || record.Definition != entry.Server.Definition() || entry.Server.Command == nil {
			continue
		}
		index, _ := npmPackageArgument(programName(*entry.Server.Command), entry.Server.Args)
		if index < 0 || entry.Server.Args[index] != record.Requested {
			continue
		}
		arguments := append([]string(nil), entry.Server.Args...)
		arguments[index] = record.Package + "@" + record.Version
		entry.Server.Args = arguments
		pinned[name] = entry
	}
	return pinned
}

// Unrecorded names the servers that have no lock record for their current
// definition.
func Unrecorded(entries Entries, records []lockfile.MCPServer) []string {
	var names []string
	for _, name := range entries.Names() {
		record, found := recordFor(records, name)
		if !found || record.Definition != entries[name].Server.Definition() {
			names = append(names, name)
		}
	}
	return names
}

func recordFor(records []lockfile.MCPServer, name string) (lockfile.MCPServer, bool) {
	for _, record := range records {
		if record.Name == name {
			return record, true
		}
	}
	return lockfile.MCPServer{}, false
}

func programName(command string) string {
	name := strings.ToLower(path.Base(strings.ReplaceAll(command, "\\", "/")))
	for _, suffix := range []string{".cmd", ".exe", ".bat"} {
		name = strings.TrimSuffix(name, suffix)
	}
	return name
}

// npmPackageArgument reports whether the command is an npm-registry package
// runner and where its package spec sits in args (-1 when it cannot be
// located, such as with --package).
func npmPackageArgument(program string, args []string) (int, bool) {
	start := 0
	switch program {
	case "npx", "bunx":
	case "pnpm", "yarn", "bun":
		subcommand := firstPositional(args, 0)
		if subcommand < 0 || args[subcommand] != "dlx" && !(program == "bun" && args[subcommand] == "x") {
			return 0, false
		}
		start = subcommand + 1
	default:
		return 0, false
	}
	for _, argument := range args[start:] {
		if argument == "--" {
			break
		}
		if argument == "-p" || argument == "-c" || argument == "--call" || argument == "--package" || strings.HasPrefix(argument, "--package=") {
			return -1, true
		}
	}
	return firstPositional(args, start), true
}

func firstPositional(args []string, start int) int {
	for index := start; index < len(args); index++ {
		if args[index] == "--" {
			if index+1 < len(args) {
				return index + 1
			}
			return -1
		}
		if !strings.HasPrefix(args[index], "-") {
			return index
		}
	}
	return -1
}

func splitNPMSpec(argument string) (name, spec string, ok bool) {
	separator := strings.LastIndex(argument, "@")
	if separator <= 0 {
		name, spec = argument, "latest"
	} else {
		name, spec = argument[:separator], argument[separator+1:]
	}
	if !npmName.MatchString(name) || spec == "" {
		return "", "", false
	}
	return name, spec, true
}

// pypiRequested reports whether the command runs a PyPI package (uvx,
// `uv tool run`, `pipx run`) and which requirement it names. The requirement
// is "" when options precede it that this parser does not model.
func pypiRequested(program string, args []string) (string, bool) {
	var words []string
	switch program {
	case "uvx":
	case "uv":
		words = []string{"tool", "run"}
	case "pipx":
		words = []string{"run"}
	default:
		return "", false
	}
	start := 0
	for _, word := range words {
		if start >= len(args) || args[start] != word {
			return "", false
		}
		start++
	}
	for index := start; index < len(args); index++ {
		if args[index] == "--from" || args[index] == "--spec" {
			if index+1 < len(args) {
				return args[index+1], true
			}
			return "", true
		}
		if value, found := strings.CutPrefix(args[index], "--from="); found {
			return value, true
		}
	}
	if start < len(args) && !strings.HasPrefix(args[start], "-") {
		return args[start], true
	}
	return "", true
}

var (
	dockerValueFlags = map[string]bool{
		"-e": true, "--env": true, "--env-file": true, "-v": true, "--volume": true, "--mount": true,
		"-p": true, "--publish": true, "--name": true, "--network": true, "--net": true, "-w": true,
		"--workdir": true, "--entrypoint": true, "-u": true, "--user": true, "-l": true, "--label": true,
		"--platform": true, "--pull": true, "-m": true, "--memory": true, "--cpus": true, "-h": true,
		"--hostname": true, "--add-host": true,
	}
	dockerSwitches = map[string]bool{
		"-i": true, "-t": true, "-it": true, "-ti": true, "-d": true, "--rm": true, "--init": true,
		"--interactive": true, "--tty": true, "--detach": true, "--read-only": true, "--privileged": true,
	}
)

// dockerImage finds the image of a `docker run` command, or "" when an option
// it does not know makes the position of the image uncertain.
func dockerImage(args []string) string {
	if len(args) == 0 || args[0] != "run" {
		return ""
	}
	for index := 1; index < len(args); index++ {
		argument := args[index]
		switch {
		case !strings.HasPrefix(argument, "-"):
			return argument
		case dockerValueFlags[argument]:
			index++
		case dockerSwitches[argument] || strings.Contains(argument, "="):
		default:
			return ""
		}
	}
	return ""
}
