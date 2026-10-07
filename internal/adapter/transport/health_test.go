package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

func TestHTTPHealthEndpoints(t *testing.T) {
	ready := true
	server := khttp.NewServer()
	registerHTTPHealth(server, func(ctx context.Context) error {
		if !ready {
			return errors.New("mongodb://secret-host unavailable")
		}
		return nil
	}, time.Second)
	host := httptest.NewServer(server)
	t.Cleanup(host.Close)

	assertProbe := func(path string, wantStatus int, wantBody string) {
		t.Helper()
		response, err := http.Get(host.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != wantStatus || string(body) != wantBody || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("GET %s = status %d body %q cache %q", path, response.StatusCode, body, response.Header.Get("Cache-Control"))
		}
	}

	assertProbe("/livez", http.StatusOK, "ok\n")
	assertProbe("/readyz", http.StatusOK, "ok\n")
	ready = false
	assertProbe("/readyz", http.StatusServiceUnavailable, "not ready\n")
}
