// Command edsim serves the enterprise directory simulator over HTTP, with
// the same contract as the real directory adapter expects:
//
//	GET /v1/accounts?email=jane.doe@example.com
//
// Used by docker compose (DIRECTORY_MODE=http) and for manual testing.
package main

import (
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/directory"
)

func main() {
	port := getenv("PORT", "8081")
	failure, _ := strconv.ParseFloat(getenv("FAILURE_RATE", "0.05"), 64)
	latency, err := time.ParseDuration(getenv("MAX_LATENCY", "600ms"))
	if err != nil {
		latency = 600 * time.Millisecond
	}
	sim := &directory.Simulator{MaxLatency: latency, FailureRate: failure}
	slog.Info("enterprise directory simulator listening", "port", port, "failure_rate", failure, "max_latency", latency)
	srv := &http.Server{Addr: ":" + port, Handler: sim.Handler(os.Getenv("API_KEY")), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("edsim", "err", err)
		os.Exit(1)
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
