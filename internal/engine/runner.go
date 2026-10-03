package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/emre-tarhan/vcflift/internal/model"
	"github.com/emre-tarhan/vcflift/internal/subprocess"
)

type Runner struct{}

func (Runner) Run(ctx context.Context, p Pipeline, progress func(model.ProgressEvent)) error {
	for start := 0; start < len(p.Steps); {
		end := start
		for end < len(p.Steps)-1 && p.Steps[end].PipeToNext {
			end++
		}
		if err := runGroup(ctx, p, start, end, progress); err != nil {
			return err
		}
		start = end + 1
	}
	return nil
}

func runGroup(ctx context.Context, p Pipeline, start, end int, progress func(model.ProgressEvent)) error {
	steps := p.Steps[start : end+1]
	cmds := make([]*exec.Cmd, len(steps))
	stderrs := make([]bytes.Buffer, len(steps))
	for i, step := range steps {
		cmd := exec.CommandContext(ctx, step.Executable, step.Args...)
		subprocess.HideConsole(cmd)
		cmd.Env = mergeEnv(os.Environ(), p.Env)
		cmd.Stderr = &stderrs[i]
		cmds[i] = cmd
	}

	for i := 0; i < len(cmds)-1; i++ {
		pipe, err := cmds[i].StdoutPipe()
		if err != nil {
			return fmt.Errorf("%s: create stdout pipe: %w", steps[i].Name, err)
		}
		cmds[i+1].Stdin = pipe
	}
	if len(cmds) == 1 {
		cmds[0].Stdout = io.Discard
	} else {
		cmds[len(cmds)-1].Stdout = io.Discard
	}

	// Pipeline processes are started downstream-to-upstream so every reader is
	// ready before its writer. Progress, however, is user-facing and should
	// describe the logical data flow rather than process-start order.
	for i := len(cmds) - 1; i >= 0; i-- {
		if err := cmds[i].Start(); err != nil {
			for j := i + 1; j < len(cmds); j++ {
				if cmds[j].Process != nil {
					_ = cmds[j].Process.Kill()
				}
			}
			return fmt.Errorf("start %s: %w", steps[i].Name, err)
		}
	}
	if progress != nil {
		for i := range steps {
			progress(model.ProgressEvent{Stage: stageForStep(steps[i].Name), Message: steps[i].Name})
		}
	}

	var firstErr error
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil && firstErr == nil {
			msg := strings.TrimSpace(stderrs[i].String())
			if msg != "" {
				firstErr = fmt.Errorf("%s failed: %w: %s", steps[i].Name, err, msg)
			} else {
				firstErr = fmt.Errorf("%s failed: %w", steps[i].Name, err)
			}
		}
	}
	return firstErr
}

func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	out := append([]string(nil), base...)
	for k, v := range overrides {
		prefix := k + "="
		replaced := false
		for i := range out {
			if strings.HasPrefix(out[i], prefix) {
				out[i] = prefix + v
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, prefix+v)
		}
	}
	return out
}

func stageForStep(name string) model.Stage {
	switch {
	case strings.HasPrefix(name, "validate source"):
		return model.StageSourceValidation
	case strings.HasPrefix(name, "validate target"):
		return model.StageTargetValidation
	case strings.Contains(name, "genotype"):
		return model.StageGenotyping
	case strings.Contains(name, "gVCF"):
		return model.StageGVCFValidation
	case strings.Contains(name, "rename") || strings.Contains(name, "extract"):
		return model.StageInspecting
	case strings.Contains(name, "hg38"):
		return model.StageSourceValidation
	case strings.Contains(name, "liftover"):
		return model.StageLiftover
	case strings.Contains(name, "hg19"):
		return model.StageTargetValidation
	case strings.Contains(name, "sort"):
		return model.StageSorting
	case strings.Contains(name, "index"):
		return model.StageIndexing
	default:
		return model.StagePreparing
	}
}
