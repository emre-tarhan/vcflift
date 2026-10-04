package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/emre-tarhan/vcflift/internal/model"
	"github.com/emre-tarhan/vcflift/internal/subprocess"
)

// defaultHeartbeat is how often a running pipeline group reports that it is
// still alive. Whole-genome liftover groups run for many minutes without a
// natural event, which looked like a hang in UIs.
const defaultHeartbeat = 30 * time.Second

type Runner struct {
	// Heartbeat overrides the heartbeat interval; zero means the default.
	// A negative value disables heartbeats entirely.
	Heartbeat time.Duration
}

func (r Runner) heartbeatInterval() time.Duration {
	switch {
	case r.Heartbeat < 0:
		return 0
	case r.Heartbeat == 0:
		return defaultHeartbeat
	default:
		return r.Heartbeat
	}
}

func (r Runner) Run(ctx context.Context, p Pipeline, progress func(model.ProgressEvent)) error {
	for start := 0; start < len(p.Steps); {
		end := start
		for end < len(p.Steps)-1 && p.Steps[end].PipeToNext {
			end++
		}
		if err := runGroup(ctx, p, start, end, r.heartbeatInterval(), progress); err != nil {
			return err
		}
		start = end + 1
	}
	return nil
}

func runGroup(ctx context.Context, p Pipeline, start, end int, heartbeat time.Duration, progress func(model.ProgressEvent)) error {
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

	// Pipes are created explicitly so the parent can drop its own copies
	// once the children are running. As long as the parent holds a read
	// end, a mid-group death leaves the upstream writer blocked on a full
	// pipe instead of failing with EPIPE — the whole group then deadlocks
	// and the UI hangs forever on "converting variants".
	var parentPipeEnds []*os.File
	for i := 0; i < len(cmds)-1; i++ {
		pr, pw, err := os.Pipe()
		if err != nil {
			return fmt.Errorf("%s: create pipe: %w", steps[i].Name, err)
		}
		cmds[i].Stdout = pw
		cmds[i+1].Stdin = pr
		parentPipeEnds = append(parentPipeEnds, pr, pw)
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
			for _, f := range parentPipeEnds {
				_ = f.Close()
			}
			return fmt.Errorf("start %s: %w", steps[i].Name, err)
		}
	}
	// The children own their dups now; without this close, a reader that
	// dies would never signal the writer (see comment above).
	for _, f := range parentPipeEnds {
		_ = f.Close()
	}
	if progress != nil {
		for i := range steps {
			progress(model.ProgressEvent{Stage: stageForStep(steps[i].Name), Message: steps[i].Name})
		}
	}

	// The sink of a piped group exits last, so its stage names the part of
	// the pipeline that is still running while the heartbeat fires.
	done := make(chan struct{})
	finished := make(chan struct{})
	if heartbeat > 0 && progress != nil {
		groupStart := time.Now()
		ticker := time.NewTicker(heartbeat)
		go func() {
			defer close(finished)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case now := <-ticker.C:
					progress(model.ProgressEvent{
						Stage:   stageForStep(steps[len(steps)-1].Name),
						Message: steps[len(steps)-1].Name,
						Elapsed: now.Sub(groupStart),
					})
				}
			}
		}()
	} else {
		close(finished)
	}

	// Wait for every command concurrently and attribute the failure to the
	// command that actually failed first in time: when a mid-group reader
	// dies, the upstream writer only fails afterwards with a broken pipe,
	// and reporting the writer would hide the real error.
	type waitResult struct {
		idx int
		err error
		at  time.Time
	}
	results := make(chan waitResult, len(cmds))
	for i, cmd := range cmds {
		go func(i int, cmd *exec.Cmd) {
			err := cmd.Wait()
			results <- waitResult{idx: i, err: err, at: time.Now()}
		}(i, cmd)
	}
	var firstFailure *waitResult
	for range cmds {
		r := <-results
		if r.err != nil && (firstFailure == nil || r.at.Before(firstFailure.at)) {
			rc := r
			firstFailure = &rc
		}
	}
	close(done)
	<-finished // no progress callback may outlive this group
	if firstFailure == nil {
		return nil
	}
	msg := strings.TrimSpace(stderrs[firstFailure.idx].String())
	if msg != "" {
		return fmt.Errorf("%s failed: %w: %s", steps[firstFailure.idx].Name, firstFailure.err, msg)
	}
	return fmt.Errorf("%s failed: %w", steps[firstFailure.idx].Name, firstFailure.err)
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
