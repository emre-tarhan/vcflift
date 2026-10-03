package vcf

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/emre-tarhan/vcflift/internal/subprocess"
)

// SummarizeSourceCallsBCFTools measures the DeepVariant candidate callset with
// the exact BCFtools expression used by the conversion pipeline. This keeps the
// conservation check independent from liftover output without re-parsing every
// full-width gVCF record in Go.
//
// InputRecords is read from a fresh adjacent TBI/CSI when available. If the
// sidecar is absent, stale, or unreadable, the function falls back to the
// portable in-process record counter.
func SummarizeSourceCallsBCFTools(ctx context.Context, bcftools, path string, threads int) (SourceCallSummary, error) {
	if bcftools == "" {
		return SourceCallSummary{}, fmt.Errorf("bcftools executable is empty")
	}
	if path == "" {
		return SourceCallSummary{}, fmt.Errorf("source VCF path is empty")
	}

	records, err := indexedRecordCount(ctx, bcftools, path)
	if err != nil {
		records, err = CountRecords(path)
		if err != nil {
			return SourceCallSummary{}, fmt.Errorf("count source records: %w", err)
		}
	}

	viewArgs := []string{"view", "-i", `GT="alt"`, "-Ou"}
	if threads > 0 {
		viewArgs = append(viewArgs, "--threads", strconv.Itoa(threads))
	}
	viewArgs = append(viewArgs, path)

	viewCmd := exec.CommandContext(ctx, bcftools, viewArgs...)
	subprocess.HideConsole(viewCmd)
	queryCmd := exec.CommandContext(ctx, bcftools, "query", "-f", "%FILTER\n", "-")
	subprocess.HideConsole(queryCmd)

	var viewErr, queryErr bytes.Buffer
	viewCmd.Stderr = &viewErr
	queryCmd.Stderr = &queryErr

	viewOut, err := viewCmd.StdoutPipe()
	if err != nil {
		return SourceCallSummary{}, fmt.Errorf("create source QC view pipe: %w", err)
	}
	queryCmd.Stdin = viewOut
	queryOut, err := queryCmd.StdoutPipe()
	if err != nil {
		return SourceCallSummary{}, fmt.Errorf("create source QC query pipe: %w", err)
	}

	// Start the downstream reader first, matching the main pipeline runner.
	if err := queryCmd.Start(); err != nil {
		return SourceCallSummary{}, fmt.Errorf("start source QC query: %w", err)
	}
	if err := viewCmd.Start(); err != nil {
		_ = queryCmd.Process.Kill()
		_ = queryCmd.Wait()
		return SourceCallSummary{}, fmt.Errorf("start source QC view: %w", err)
	}

	nonRef, pass, scanErr := countFilterStream(queryOut)
	viewWaitErr := viewCmd.Wait()
	queryWaitErr := queryCmd.Wait()

	if scanErr != nil {
		return SourceCallSummary{}, fmt.Errorf("read source QC stream: %w", scanErr)
	}
	if viewWaitErr != nil {
		msg := strings.TrimSpace(viewErr.String())
		if msg != "" {
			return SourceCallSummary{}, fmt.Errorf("source QC view failed: %w: %s", viewWaitErr, msg)
		}
		return SourceCallSummary{}, fmt.Errorf("source QC view failed: %w", viewWaitErr)
	}
	if queryWaitErr != nil {
		msg := strings.TrimSpace(queryErr.String())
		if msg != "" {
			return SourceCallSummary{}, fmt.Errorf("source QC query failed: %w: %s", queryWaitErr, msg)
		}
		return SourceCallSummary{}, fmt.Errorf("source QC query failed: %w", queryWaitErr)
	}

	return SourceCallSummary{
		Records:            records,
		NonReferenceCalls:  nonRef,
		NonReferencePASS:   pass,
		LiftoverCandidates: nonRef,
	}, nil
}

func indexedRecordCount(ctx context.Context, bcftools, path string) (int64, error) {
	indexPath, _ := DiscoverSidecarIndex(path)
	if indexPath == "" || !SidecarIndexFresh(path, indexPath) {
		return 0, fmt.Errorf("fresh adjacent source index unavailable")
	}
	cmd := exec.CommandContext(ctx, bcftools, "index", "-n", path)
	subprocess.HideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("read record count from source index: %w", err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("parse indexed source record count %q", strings.TrimSpace(string(out)))
	}
	return n, nil
}

func countFilterStream(r io.Reader) (records, pass int64, err error) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	for s.Scan() {
		filter := strings.TrimSpace(s.Text())
		if filter == "" {
			continue
		}
		records++
		if filter == "PASS" {
			pass++
		}
	}
	return records, pass, s.Err()
}
