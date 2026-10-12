package directory

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"
)

// Simulator is a deterministic fake enterprise directory. The same e-mail
// always gets the same site, business unit and GPS code, so demos and tests
// are reproducible. Markers in the local part (tokens separated by . + - _,
// e.g. "jane.ko@example.com") trigger edge cases:
//
//	"ko"      -> account status KO            (functional KO)
//	"nogps"   -> empty GPS code                (functional KO)
//	"ghost"   -> unknown account, HTTP 404     (functional KO)
//	"edslow"  -> hangs until the caller's timeout (technical KO -> buffer)
//	"ederr"   -> HTTP 503                      (technical KO -> buffer)
//
// On top of that, FailureRate randomly returns HTTP 503 / 500 / timeouts to
// feed the technical KPIs.
type Simulator struct {
	MaxLatency  time.Duration
	FailureRate float64
}

// sites are random-looking trigrams used as attachment sites.
var sites = []string{"PRS", "LYN", "CRG", "BGY", "MUN", "DTW", "SHG", "BLR", "PRG", "CAI", "MXC", "SPL"}

// Lookup implements Client.
func (s *Simulator) Lookup(ctx context.Context, email string) (Account, error) {
	// Edge cases driven by the e-mail.
	switch {
	case HasMarker(email, "edslow"):
		<-ctx.Done()
		return Account{}, ctx.Err()
	case HasMarker(email, "ederr"):
		return Account{}, &HTTPError{Code: 503, Body: "directory backend unavailable"}
	case HasMarker(email, "ghost"):
		return Account{}, ErrNotFound
	}

	// Random technical failures.
	if s.FailureRate > 0 && rand.Float64() < s.FailureRate {
		switch rand.IntN(3) {
		case 0:
			return Account{}, &HTTPError{Code: 503, Body: "directory backend unavailable"}
		case 1:
			return Account{}, &HTTPError{Code: 500, Body: "internal error"}
		default:
			<-ctx.Done()
			return Account{}, ctx.Err()
		}
	}

	// Latency.
	if s.MaxLatency > 0 {
		select {
		case <-time.After(time.Duration(rand.Int64N(int64(s.MaxLatency)))):
		case <-ctx.Done():
			return Account{}, ctx.Err()
		}
	}
	return s.Profile(email), nil
}

// Profile derives the deterministic account of an e-mail (site, business
// unit, GPS code, status) without simulating latency or failures.
func (s *Simulator) Profile(email string) Account {
	email = strings.ToLower(email)
	h := fnv.New64a()
	_, _ = h.Write([]byte(email))
	sum := h.Sum64()

	acc := Account{
		Email:        email,
		Status:       "OK",
		Site:         sites[sum%uint64(len(sites))],
		BusinessUnit: fmt.Sprintf("%03d", 100+(sum>>8)%900),
		GPSCode:      fmt.Sprintf("%08x", uint32(sum>>20)),
	}
	if HasMarker(email, "ko") {
		acc.Status = "KO"
	}
	if HasMarker(email, "nogps") {
		acc.GPSCode = ""
	}
	return acc
}

// HasMarker reports whether the local part of an e-mail contains a marker
// token (tokens are separated by '.', '+', '-' or '_').
func HasMarker(email, marker string) bool {
	local, _, _ := strings.Cut(strings.ToLower(email), "@")
	for _, t := range strings.FieldsFunc(local, func(r rune) bool { return r == '.' || r == '+' || r == '-' || r == '_' }) {
		if t == marker {
			return true
		}
	}
	return false
}

// Handler exposes the simulator over HTTP with the real contract, so the
// HTTP client can be exercised end to end (cmd/edsim, docker compose).
func (s *Simulator) Handler(apiKey string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/accounts", func(w http.ResponseWriter, r *http.Request) {
		if apiKey != "" && r.Header.Get("Authorization") != "Bearer "+apiKey {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		email := r.URL.Query().Get("email")
		if !strings.Contains(email, "@") {
			http.Error(w, `{"error":"email query parameter required"}`, http.StatusBadRequest)
			return
		}
		acc, err := s.Lookup(r.Context(), email)
		w.Header().Set("Content-Type", "application/json")
		switch e := err.(type) {
		case nil:
			_ = json.NewEncoder(w).Encode(acc)
		case *HTTPError:
			w.WriteHeader(e.Code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": e.Body})
		default:
			if IsNotFound(err) {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
				return
			}
			w.WriteHeader(http.StatusGatewayTimeout)
		}
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	return mux
}
