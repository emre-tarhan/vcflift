package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emre-tarhan/vcflift/internal/model"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("VCFLIFT_HELPER") != "1" {
		return
	}
	args := os.Args
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || sep+1 >= len(args) {
		os.Exit(3)
	}
	mode := args[sep+1]
	switch mode {
	case "emit":
		fmt.Fprint(os.Stdout, "hello")
	case "upper":
		b, _ := io.ReadAll(os.Stdin)
		fmt.Fprint(os.Stdout, strings.ToUpper(string(b)))
	case "sink":
		b, _ := io.ReadAll(os.Stdin)
		if string(b) != "HELLO" {
			fmt.Fprintln(os.Stderr, "unexpected:", string(b))
			os.Exit(4)
		}
	case "fail":
		fmt.Fprint(os.Stderr, "intentional failure")
		os.Exit(5)
	case "sleep":
		time.Sleep(400 * time.Millisecond)
	case "forever":
		// Writes until the reader disappears; SIGPIPE (or a write error)
		// ends the process. Used to prove the writer cannot deadlock.
		for {
			if _, err := fmt.Fprint(os.Stdout, strings.Repeat("x", 4096)); err != nil {
				os.Exit(0)
			}
		}
	case "exit0":
		os.Exit(0)
	default:
		os.Exit(6)
	}
	os.Exit(0)
}

func helperStep(name, mode string, pipe bool) Step {
	return Step{Name: name, Executable: os.Args[0], Args: []string{"-test.run=TestHelperProcess", "--", mode}, PipeToNext: pipe}
}

func TestRunnerPipesWithoutShell(t *testing.T) {
	t.Setenv("VCFLIFT_HELPER", "1")
	p := Pipeline{Steps: []Step{
		helperStep("emit", "emit", true),
		helperStep("upper", "upper", true),
		helperStep("sink", "sink", false),
	}}
	if err := (Runner{}).Run(context.Background(), p, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerReturnsStepStderr(t *testing.T) {
	t.Setenv("VCFLIFT_HELPER", "1")
	p := Pipeline{Steps: []Step{helperStep("broken step", "fail", false)}}
	err := (Runner{}).Run(context.Background(), p, nil)
	if err == nil || !strings.Contains(err.Error(), "intentional failure") {
		t.Fatalf("err=%v", err)
	}
}

func TestRunnerProgressUsesLogicalPipelineOrder(t *testing.T) {
	t.Setenv("VCFLIFT_HELPER", "1")
	p := Pipeline{Steps: []Step{
		helperStep("emit", "emit", true),
		helperStep("upper", "upper", true),
		helperStep("sink", "sink", false),
	}}
	var got []string
	if err := (Runner{}).Run(context.Background(), p, func(e model.ProgressEvent) {
		got = append(got, e.Message)
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"emit", "upper", "sink"}
	if len(got) != len(want) {
		t.Fatalf("progress=%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("progress=%v want=%v", got, want)
		}
	}
}

func TestRunnerEmitsHeartbeatForLongGroups(t *testing.T) {
	t.Setenv("VCFLIFT_HELPER", "1")
	p := Pipeline{Steps: []Step{helperStep("slow sort", "sleep", false)}}
	var mu sync.Mutex
	var heartbeats []model.ProgressEvent
	err := (Runner{Heartbeat: 50 * time.Millisecond}).Run(context.Background(), p, func(e model.ProgressEvent) {
		if e.Elapsed > 0 {
			mu.Lock()
			heartbeats = append(heartbeats, e)
			mu.Unlock()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(heartbeats) < 3 {
		t.Fatalf("heartbeats=%d, want at least 3", len(heartbeats))
	}
	for _, e := range heartbeats {
		if e.Stage != model.StageSorting || e.Message != "slow sort" {
			t.Fatalf("heartbeat=%+v, want stage=%s message=%q", e, model.StageSorting, "slow sort")
		}
	}
}

func TestRunnerHeartbeatDisabled(t *testing.T) {
	t.Setenv("VCFLIFT_HELPER", "1")
	p := Pipeline{Steps: []Step{helperStep("slow sort", "sleep", false)}}
	var mu sync.Mutex
	heartbeats := 0
	err := (Runner{Heartbeat: -1}).Run(context.Background(), p, func(e model.ProgressEvent) {
		if e.Elapsed > 0 {
			mu.Lock()
			heartbeats++
			mu.Unlock()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if heartbeats != 0 {
		t.Fatalf("heartbeats=%d, want 0 when disabled", heartbeats)
	}
}

// Regression: a mid-group reader that exits while the upstream writer still
// has data used to deadlock the writer on the full pipe (the parent held the
// read end open), hanging the whole conversion on "converting variants".
func TestRunnerDoesNotDeadlockWhenMidStepExitsEarly(t *testing.T) {
	t.Setenv("VCFLIFT_HELPER", "1")
	p := Pipeline{Steps: []Step{
		helperStep("endless writer", "forever", true),
		helperStep("early exit reader", "exit0", false),
	}}
	done := make(chan error, 1)
	go func() {
		done <- (Runner{Heartbeat: -1}).Run(context.Background(), p, nil)
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "endless writer failed") {
			t.Fatalf("err=%v, want the broken writer to be reported", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runner deadlocked: group never finished after the reader exited")
	}
}

// The step that fails first in time is the real failure; the upstream writer
// that dies from the resulting broken pipe must not mask it.
func TestRunnerAttributesErrorToFirstFailure(t *testing.T) {
	t.Setenv("VCFLIFT_HELPER", "1")
	p := Pipeline{Steps: []Step{
		helperStep("upstream writer", "emit", true),
		helperStep("real failure", "fail", false),
	}}
	err := (Runner{Heartbeat: -1}).Run(context.Background(), p, nil)
	if err == nil || !strings.Contains(err.Error(), "real failure failed") || !strings.Contains(err.Error(), "intentional failure") {
		t.Fatalf("err=%v, want the failing step with its stderr", err)
	}
}
