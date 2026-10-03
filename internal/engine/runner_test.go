package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

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
