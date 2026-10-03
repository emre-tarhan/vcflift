package httpretry

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestDoRetriesTransientStatus(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	resp, err := Do(context.Background(), srv.Client(), func() (*http.Request, error) {
		return http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if calls != 3 {
		t.Fatalf("want 3 attempts, got %d", calls)
	}
}

func TestDoDoesNotRetryClientError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.Client(), func() (*http.Request, error) {
		return http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	})
	var status *StatusError
	if !errors.As(err, &status) || status.Code != http.StatusNotFound {
		t.Fatalf("want StatusError 404, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("want 1 attempt, got %d", calls)
	}
}

type failingDoer struct {
	fails int
	calls int32
}

func (f *failingDoer) Do(*http.Request) (*http.Response, error) {
	if atomic.AddInt32(&f.calls, 1) <= int32(f.fails) {
		return nil, errors.New("dial tcp: connection reset by peer")
	}
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

func TestDoRetriesNetworkError(t *testing.T) {
	d := &failingDoer{fails: 2}
	resp, err := Do(context.Background(), d, func() (*http.Request, error) {
		return http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.invalid", nil)
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if d.calls != 3 {
		t.Fatalf("want 3 attempts, got %d", d.calls)
	}
}

func TestRetryableClassification(t *testing.T) {
	if Retryable(nil) {
		t.Fatal("nil must not be retryable")
	}
	if Retryable(context.Canceled) {
		t.Fatal("canceled context must not be retryable")
	}
	if Retryable(&StatusError{Code: 404}) {
		t.Fatal("404 must not be retryable")
	}
	if !Retryable(&StatusError{Code: 503}) {
		t.Fatal("503 must be retryable")
	}
	if !Retryable(errors.New("unexpected EOF")) {
		t.Fatal("stream error must be retryable")
	}
	if Retryable(&fs.PathError{Op: "write", Path: "x", Err: errors.New("no space left on device")}) {
		t.Fatal("local filesystem errors must not be retryable")
	}
}
