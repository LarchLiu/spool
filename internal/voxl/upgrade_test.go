package voxl

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLatestVoxlReleaseSelectsStableNonYankedSemanticMaximum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") == "" {
			t.Error("registry request omitted JSON or client identification")
		}
		fmt.Fprint(w, `{"versions":[
			{"num":"0.3.9"}, {"num":"0.3.10"}, {"num":"0.2.99"},
			{"num":"9.0.0","yanked":true}, {"num":"1.0.0-rc.1"},
			{"num":"invalid"}
		]}`)
	}))
	defer server.Close()
	version, err := fetchLatestVoxlRelease(t.Context(), server.Client(), server.URL)
	if err != nil || version != "0.3.10" {
		t.Fatal("incorrect stable upstream release", version, err)
	}
}

func TestLatestVoxlReleaseRejectsFailedInvalidAndOversizedResponses(t *testing.T) {
	for _, scenario := range []string{"http-error", "invalid-json", "no-stable", "oversized", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "http-error":
					w.WriteHeader(http.StatusServiceUnavailable)
				case "invalid-json":
					fmt.Fprint(w, "not JSON")
				case "oversized":
					fmt.Fprint(w, strings.Repeat(" ", maxReleaseMetadata+1))
				default:
					fmt.Fprint(w, `{"versions":[{"num":"1.0.0-beta"},{"num":"0.4.0","yanked":true}]}`)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "canceled" {
				cancel()
			}
			if version, err := fetchLatestVoxlRelease(ctx, server.Client(), server.URL); err == nil || version != "" {
				t.Fatal("unusable registry response was accepted", version, err)
			}
		})
	}
}
