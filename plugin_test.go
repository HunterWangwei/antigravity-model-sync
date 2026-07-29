package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractAccessToken(t *testing.T) {
	if got := extractAccessToken(map[string]any{"access_token": " metadata-token "}, []byte(`{"access_token":"stored-token"}`)); got != "metadata-token" {
		t.Fatalf("got %q", got)
	}
	if got := extractAccessToken(nil, []byte(`{"token":{"access_token":"nested-token"}}`)); got != "nested-token" {
		t.Fatalf("got %q", got)
	}
}

func TestMergeStaticWinsAndRemoteFillsMissing(t *testing.T) {
	static := []modelInfo{{ID: "Model-A", DisplayName: "Static", ContextLength: 0}}
	remote := []modelInfo{{ID: "model-a", DisplayName: "Remote", ContextLength: 123}, {ID: "model-b", DisplayName: "New"}}
	got := mergeModels(static, remote)
	if len(got) != 2 || got[0].DisplayName != "Static" || got[0].ContextLength != 123 || got[1].ID != "model-b" {
		t.Fatalf("unexpected merge: %#v", got)
	}
}

func TestFetchFallbackAndTokenNotReturned(t *testing.T) {
	calls := 0
	caller := func(_ string, payload []byte) ([]byte, error) {
		calls++
		if !strings.Contains(string(payload), "Bearer secret-token") {
			t.Fatal("authorization missing")
		}
		status := 503
		body := []byte(`{}`)
		if calls == 2 {
			status = 200
			body = []byte(`{"models":{"new-model":{"displayName":"New Model","maxTokens":100,"maxOutputTokens":20}}}`)
		}
		return okEnvelope(httpResponse{StatusCode: status, Body: body})
	}
	got := fetchRemoteModels(caller, "callback", "secret-token", nil, nil, []string{"https://one", "https://two"})
	if calls != 2 || len(got) != 1 || got[0].ContextLength != 100 {
		t.Fatalf("calls=%d models=%#v", calls, got)
	}
}

func TestNonAntigravityIgnored(t *testing.T) {
	raw, err := handleMethod("model.for_auth", []byte(`{"AuthProvider":"codex"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	var result modelResponse
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Provider != "" || len(result.Models) != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestInvalidJSONDoesNotEchoSecret(t *testing.T) {
	_, err := handleMethod("model.for_auth", []byte(`{"access_token":"secret-token"`), nil)
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestParseEndpoints(t *testing.T) {
	got := parseEndpoints([]byte("enabled: true\nendpoints:\n  - https://one.example/\n  - 'https://two.example'\npriority: 100\n"))
	if len(got) != 2 || got[0] != "https://one.example" || got[1] != "https://two.example" {
		t.Fatalf("got %#v", got)
	}
}
