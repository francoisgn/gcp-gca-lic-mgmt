package directory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"
)

func TestSimulatorIsDeterministic(t *testing.T) {
	s := &Simulator{}
	a, err := s.Lookup(context.Background(), "jane.doe@example.com")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.Lookup(context.Background(), "JANE.DOE@example.com")
	if a != b {
		t.Fatalf("not deterministic: %+v vs %+v", a, b)
	}
	if a.Status != "OK" || !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(a.Site) ||
		!regexp.MustCompile(`^\d{3}$`).MatchString(a.BusinessUnit) || !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(a.GPSCode) {
		t.Fatalf("unexpected account %+v", a)
	}
}

func TestSimulatorMarkers(t *testing.T) {
	s := &Simulator{}
	ctx := context.Background()
	if a, _ := s.Lookup(ctx, "john.ko@example.com"); a.Status != "KO" {
		t.Error("ko marker")
	}
	if a, _ := s.Lookup(ctx, "kowalski@example.com"); a.Status != "OK" {
		t.Error("'ko' inside a name must not trigger the marker")
	}
	if a, _ := s.Lookup(ctx, "emma.nogps@example.com"); a.GPSCode != "" {
		t.Error("nogps marker")
	}
	if _, err := s.Lookup(ctx, "max.ghost@example.com"); !IsNotFound(err) {
		t.Error("ghost marker")
	}
	if _, err := s.Lookup(ctx, "a.ederr@example.com"); err == nil {
		t.Error("ederr marker")
	}
	tctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := s.Lookup(tctx, "a.edslow@example.com"); err == nil {
		t.Error("edslow marker")
	}
}

// The HTTP client and the simulator handler implement the same contract.
func TestHTTPClientAgainstSimulator(t *testing.T) {
	srv := httptest.NewServer((&Simulator{}).Handler("secret"))
	defer srv.Close()
	ctx := context.Background()
	c := NewHTTPClient(HTTPOptions{BaseURL: srv.URL, APIKey: "secret"})
	a, err := c.Lookup(ctx, "jane.doe@example.com")
	if err != nil || a.Email != "jane.doe@example.com" {
		t.Fatalf("lookup: %+v %v", a, err)
	}
	if _, err := c.Lookup(ctx, "max.ghost@example.com"); !IsNotFound(err) {
		t.Fatalf("404 not mapped to ErrNotFound: %v", err)
	}
	var he *HTTPError
	if _, err := c.Lookup(ctx, "a.ederr@example.com"); err == nil {
		t.Fatal("503 not reported")
	} else if e, ok := err.(*HTTPError); !ok || e.Code != 503 {
		t.Fatalf("err = %v (%T)", err, he)
	}
	if _, err := NewHTTPClient(HTTPOptions{BaseURL: srv.URL, APIKey: "wrong"}).Lookup(ctx, "jane.doe@example.com"); err == nil {
		t.Fatal("bad API key accepted")
	}
}

// The field mapping and the path template adapt to another directory contract.
func TestHTTPClientMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/people/jane.doe@example.com" || r.Header.Get("X-API-Key") != "k" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"state":"OK","org":{"site":"PRS","bu":412,"gps":"a3f09c1e"}}`))
	}))
	defer srv.Close()
	c := NewHTTPClient(HTTPOptions{BaseURL: srv.URL, Path: "/people/{email}", AuthType: "header", AuthHeader: "X-API-Key", APIKey: "k",
		Fields: Fields{Status: "state", Site: "org.site", BusinessUnit: "org.bu", GPSCode: "org.gps"}})
	a, err := c.Lookup(context.Background(), "jane.doe@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != "OK" || a.Site != "PRS" || a.BusinessUnit != "412" || a.GPSCode != "a3f09c1e" {
		t.Fatalf("mapped account = %+v", a)
	}
}
