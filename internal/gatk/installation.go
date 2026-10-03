package gatk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/emre-tarhan/vcflift/internal/subprocess"
)

var ErrNotFound = errors.New("GATK runtime not found")

var javaVersionRE = regexp.MustCompile(`(?m)version\s+"([0-9]+)(?:[._][^"]*)?"`)

type Installation struct {
	Java        string
	Jar         string
	Version     string
	JavaVersion string
	Source      string
}

func ResolveInstallation() Installation {
	java := os.Getenv("VCFLIFT_JAVA")
	jar := os.Getenv("VCFLIFT_GATK_JAR")
	if java == "" {
		if p, err := exec.LookPath("java"); err == nil {
			java = p
		}
	}
	if jar == "" {
		if exe, err := os.Executable(); err == nil {
			base := filepath.Dir(exe)
			for _, candidate := range []string{
				filepath.Join(base, "engine", "gatk", "gatk-package.jar"),
				filepath.Join(base, "gatk-package.jar"),
			} {
				if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() {
					jar = candidate
					break
				}
			}
		}
	}
	source := "external"
	if java == "" && jar == "" {
		source = ""
	}
	return Installation{Java: java, Jar: jar, Source: source}
}

func Validate(ctx context.Context, inst Installation) (Installation, error) {
	if inst.Java == "" || inst.Jar == "" {
		return inst, ErrNotFound
	}
	if st, err := os.Stat(inst.Jar); err != nil || !st.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return inst, fmt.Errorf("GATK jar unavailable at %q: %w", inst.Jar, err)
	}
	javaCmd := exec.CommandContext(ctx, inst.Java, "-version")
	subprocess.HideConsole(javaCmd)
	javaOut, err := javaCmd.CombinedOutput()
	if err != nil {
		return inst, fmt.Errorf("Java preflight failed: %w: %s", err, strings.TrimSpace(string(javaOut)))
	}
	inst.JavaVersion = strings.TrimSpace(string(javaOut))
	major := javaMajor(inst.JavaVersion)
	if major != "17" {
		if major == "" {
			return inst, fmt.Errorf("could not determine Java major version; GATK requires Java 17: %s", inst.JavaVersion)
		}
		return inst, fmt.Errorf("unsupported Java major version %s; pinned GATK runtime requires Java 17", major)
	}

	cmd := exec.CommandContext(ctx, inst.Java, "-jar", inst.Jar, "--version")
	subprocess.HideConsole(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return inst, fmt.Errorf("GATK preflight failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	inst.Version = strings.TrimSpace(string(out))
	if inst.Version == "" {
		inst.Version = "GATK (version output empty)"
	}
	if inst.Source == "" {
		inst.Source = "external"
	}
	return inst, nil
}

func javaMajor(s string) string {
	m := javaVersionRE.FindStringSubmatch(s)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

func JavaExecutableName() string {
	if runtime.GOOS == "windows" {
		return "java.exe"
	}
	return "java"
}
