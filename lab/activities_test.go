package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFetchMetrics(t *testing.T) {
	const fakeMetrics = "# HELP node_cpu_seconds_total\nnode_cpu_seconds_total{cpu=\"0\",mode=\"idle\"} 12345.0\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		fmt.Fprint(w, fakeMetrics)
	}))
	t.Cleanup(srv.Close)

	a := &Activities{HTTP: srv.Client(), MetricsURL: srv.URL}
	got, err := a.FetchMetrics(context.Background())
	require.NoError(t, err)
	require.Equal(t, fakeMetrics, got)
}

func TestAnalyzeMetrics(t *testing.T) {
	const wantSummary = "System looks healthy."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/messages", r.URL.Path)

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "claude-haiku-4-5", body["model"])
		msgs := body["messages"].([]any)
		require.Len(t, msgs, 1)
		msg := msgs[0].(map[string]any)
		require.Equal(t, "user", msg["role"])
		// content can be string or array depending on SDK version
		require.NotNil(t, msg["content"])

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":         "msg_test",
			"type":       "message",
			"role":       "assistant",
			"model":      "claude-haiku-4-5",
			"stop_reason": "end_turn",
			"content": []map[string]any{
				{"type": "text", "text": wantSummary},
			},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
		})
	}))
	t.Cleanup(srv.Close)

	a := NewActivities(srv.Client(), "", srv.URL, "claude-haiku-4-5")
	got, err := a.AnalyzeMetrics(context.Background(), "some metrics")
	require.NoError(t, err)
	require.Equal(t, wantSummary, got)
}

func TestAnalyzeMetrics_AIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"type":    "error",
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": "no route found for model",
			},
		})
	}))
	t.Cleanup(srv.Close)

	a := NewActivities(srv.Client(), "", srv.URL, "claude-haiku-4-5")
	_, err := a.AnalyzeMetrics(context.Background(), "some metrics")
	require.Error(t, err)
}
