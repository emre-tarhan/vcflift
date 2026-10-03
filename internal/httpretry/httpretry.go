// Package httpretry adds bounded retry with exponential backoff for transient
// network failures in VCF Lift's first-use downloads. Retries are deliberate:
// resumable .part transfers make re-attempts cheap, while HTTP 4xx errors
// (bad URL, missing asset) and cancelled contexts fail immediately.
package httpretry

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"
)

// Attempts is the total number of tries per HTTP operation (first try plus
// Attempts-1 retries).
const Attempts = 4

// StatusError marks a failed HTTP response.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return "HTTP " + strconv.Itoa(e.Code) }

// TransientStatus reports HTTP status codes worth retrying: request timeouts,
// rate limiting and server-side hiccups.
func TransientStatus(code int) bool {
	return code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500
}

// Retryable classifies an error from a request or stream. Network-level
// errors and transient statuses are retryable; request-construction errors,
// 4xx statuses, local filesystem errors and cancelled contexts are not.
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var status *StatusError
	if errors.As(err, &status) {
		return TransientStatus(status.Code)
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return false
	}
	return true
}

// Sleep waits before the retry identified by attempt (0-based), with
// exponential backoff and jitter, returning early when ctx is cancelled.
func Sleep(ctx context.Context, attempt int) error {
	base := time.Second << attempt
	if base > 8*time.Second {
		base = 8 * time.Second
	}
	d := base + time.Duration(rand.Int63n(int64(time.Second)))
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Doer is the subset of *http.Client used here.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Do performs an HTTP request, retrying transient failures. It returns only
// 2xx responses; other status codes come back as *StatusError (transient ones
// are retried first). newReq is called once per attempt so request state is
// always fresh. The returned response must be closed by the caller.
func Do(ctx context.Context, client Doer, newReq func() (*http.Request, error)) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < Attempts; attempt++ {
		req, err := newReq()
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		var retryable bool
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			lastErr, retryable = err, true
		} else if resp.StatusCode/100 == 2 {
			return resp, nil
		} else {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
			resp.Body.Close()
			status := &StatusError{Code: resp.StatusCode}
			lastErr, retryable = status, TransientStatus(resp.StatusCode)
		}
		if !retryable {
			return nil, lastErr
		}
		if attempt < Attempts-1 {
			if serr := Sleep(ctx, attempt); serr != nil {
				return nil, serr
			}
		}
	}
	return nil, lastErr
}
