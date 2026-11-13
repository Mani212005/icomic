package gen_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"icomic/backend/gen"
)

func TestAIClientReadsProviderSpendFromTheAIService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/internal/v1/provider-spend" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"enabled":true,"cap_usd":2,"spent_usd":0.37,"remaining_usd":1.63,"calls":5,"uncertain_calls":0}`))
	}))
	defer server.Close()
	spend, err := gen.NewAIServiceClient(server.URL, time.Second).ProviderSpend(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !spend.Enabled || *spend.CapUsd != 2 || *spend.SpentUsd != 0.37 || *spend.RemainingUsd != 1.63 || *spend.Calls != 5 {
		t.Fatalf("unexpected spend: %+v", spend)
	}
}

func TestAIClientReadinessFollowsTheServiceStatus(t *testing.T) {
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"status":"x","checks":{}}`))
	}))
	defer server.Close()
	client := gen.NewAIServiceClient(server.URL, time.Second)
	if err := client.CheckReady(context.Background()); err != nil {
		t.Fatalf("a ready service must pass: %v", err)
	}
	status = http.StatusServiceUnavailable
	if err := client.CheckReady(context.Background()); err == nil {
		t.Fatal("a 503 must fail readiness")
	}
	server.Close()
	if err := client.CheckReady(context.Background()); err == nil {
		t.Fatal("an unreachable service must fail readiness")
	}
}
