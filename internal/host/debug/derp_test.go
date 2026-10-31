package debug

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tonhe/headsup/internal/host"
)

func TestDERPMapFetch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/derp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"Regions":{"1":{"RegionID":1,"RegionCode":"nyc","Nodes":[]}}}`))
	})
	s := httptest.NewServer(mux)
	defer s.Close()
	c := New(strings.TrimPrefix(s.URL, "http://"))
	b, ct, err := c.DERPMap(context.Background())
	if err != nil || !strings.Contains(string(b), "nyc") || !strings.Contains(ct, "json") {
		t.Fatalf("DERPMap: %v %q %s", err, ct, b)
	}

	empty := httptest.NewServer(http.NotFoundHandler())
	defer empty.Close()
	_, _, err = New(strings.TrimPrefix(empty.URL, "http://")).DERPMap(context.Background())
	if !errors.Is(err, host.ErrUnsupportedShape) {
		t.Errorf("a server without /debug/derp should be unsupported, got %v", err)
	}
}
