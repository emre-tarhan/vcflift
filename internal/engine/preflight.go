package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/emre-tarhan/vcflift/internal/subprocess"
)

const RequiredBCFToolsVersion = "1.24"

var ErrEngineNotFound = errors.New("bcftools executable not found")

type Installation struct {
	BCFTools  string `json:"bcftools"`
	PluginDir string `json:"plugin_dir"`
	Version   string `json:"version"`
}

// ResolveExplicitInstallation only returns locations that are
// selected by VCF Lift configuration: environment overrides or an adjacent
// sidecar engine directory. It does not search PATH, because
// release builds must prefer their pinned embedded engine over an arbitrary
// system bcftools installation.
func ResolveExplicitInstallation() Installation {
	bcf := os.Getenv("VCFLIFT_BCFTOOLS")
	pluginDir := os.Getenv("BCFTOOLS_PLUGINS")
	if bcf == "" {
		if exe, err := os.Executable(); err == nil {
			name := "bcftools"
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			candidate := filepath.Join(filepath.Dir(exe), "engine", name)
			if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() {
				bcf = candidate
				if pluginDir == "" {
					pluginDir = filepath.Join(filepath.Dir(exe), "engine", "plugins")
				}
			}
		}
	}
	return Installation{BCFTools: bcf, PluginDir: pluginDir}
}

func ResolvePathInstallation() Installation {
	if p, err := exec.LookPath("bcftools"); err == nil {
		return Installation{BCFTools: p, PluginDir: os.Getenv("BCFTOOLS_PLUGINS")}
	}
	return Installation{}
}

// ResolveInstallation preserves the CLI preflight behavior: explicit selection
// first, then PATH. Conversion itself uses the stricter priority implemented by
// converter.NativeConverter.
func ResolveInstallation() Installation {
	if inst := ResolveExplicitInstallation(); inst.BCFTools != "" {
		return inst
	}
	return ResolvePathInstallation()
}

func ValidateInstallation(ctx context.Context, inst Installation) (Installation, error) {
	if inst.BCFTools == "" {
		return inst, ErrEngineNotFound
	}
	overrides := map[string]string{}
	if inst.PluginDir != "" {
		overrides["BCFTOOLS_PLUGINS"] = inst.PluginDir
	}
	env := mergeEnv(os.Environ(), overrides)
	cmd := exec.CommandContext(ctx, inst.BCFTools, "--version")
	subprocess.HideConsole(cmd)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return inst, fmt.Errorf("bcftools preflight failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 0 {
		inst.Version = lines[0]
	}

	cmd = exec.CommandContext(ctx, inst.BCFTools, "plugin", "-l")
	subprocess.HideConsole(cmd)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		return inst, fmt.Errorf("cannot list bcftools plugins: %w: %s", err, strings.TrimSpace(string(out)))
	}
	found := false
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "liftover" {
			found = true
			break
		}
	}
	if !found {
		return inst, fmt.Errorf("bcftools liftover plugin not found; BCFTOOLS_PLUGINS=%q", inst.PluginDir)
	}

	cmd = exec.CommandContext(ctx, inst.BCFTools, "+liftover", "-h")
	subprocess.HideConsole(cmd)
	cmd.Env = env
	out, _ = cmd.CombinedOutput()
	help := string(out)
	for _, required := range []string{"--write-src", "--write-reject", "--lift-end"} {
		if !strings.Contains(help, required) {
			return inst, fmt.Errorf("bcftools liftover plugin is too old or incompatible: missing %s", required)
		}
	}
	return inst, nil
}

func RequireVersion(inst Installation, required string) error {
	if required == "" {
		return nil
	}
	got := ParseBCFToolsVersion(inst.Version)
	if got == "" {
		return fmt.Errorf("could not parse bcftools version from %q", inst.Version)
	}
	if got != required {
		return fmt.Errorf("unsupported bcftools version %s; VCF Lift engine bundles are pinned to %s", got, required)
	}
	return nil
}

func ParseBCFToolsVersion(line string) string {
	fields := strings.Fields(strings.TrimSpace(line))
	for i, field := range fields {
		if strings.EqualFold(field, "bcftools") && i+1 < len(fields) {
			return strings.TrimPrefix(fields[i+1], "v")
		}
	}
	if len(fields) > 0 {
		candidate := strings.TrimPrefix(fields[len(fields)-1], "v")
		if candidate != "" && candidate[0] >= '0' && candidate[0] <= '9' {
			return candidate
		}
	}
	return ""
}
