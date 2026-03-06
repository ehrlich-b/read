package relay

import (
	"net/http/httptest"
	"testing"
)

func testStore(t *testing.T) *RelayStore {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	store := testStore(t)
	srv := NewServer(store)
	ts := httptest.NewServer(srv)
	t.Cleanup(func() { ts.Close() })
	return srv, ts
}
