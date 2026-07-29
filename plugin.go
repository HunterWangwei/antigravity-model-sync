package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const (
	pluginName = "antigravity-model-sync"
	provider   = "antigravity"
	modelsPath = "/v1internal:fetchAvailableModels"
)

var defaultEndpoints = []string{
	"https://daily-cloudcode-pa.googleapis.com",
	"https://cloudcode-pa.googleapis.com",
}

//go:embed static_models.json
var staticModelsJSON []byte

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

type authModelRequest struct {
	AuthID         string            `json:"AuthID"`
	AuthProvider   string            `json:"AuthProvider"`
	StorageJSON    []byte            `json:"StorageJSON"`
	Metadata       map[string]any    `json:"Metadata"`
	Attributes     map[string]string `json:"Attributes"`
	HostCallbackID string            `json:"host_callback_id"`
}

type modelResponse struct {
	Provider string      `json:"Provider"`
	Models   []modelInfo `json:"Models"`
}

type modelInfo struct {
	ID                         string           `json:"ID"`
	Object                     string           `json:"Object,omitempty"`
	OwnedBy                    string           `json:"OwnedBy,omitempty"`
	Type                       string           `json:"Type,omitempty"`
	DisplayName                string           `json:"DisplayName,omitempty"`
	Name                       string           `json:"Name,omitempty"`
	Description                string           `json:"Description,omitempty"`
	InputTokenLimit            int64            `json:"InputTokenLimit,omitempty"`
	OutputTokenLimit           int64            `json:"OutputTokenLimit,omitempty"`
	SupportedGenerationMethods []string         `json:"SupportedGenerationMethods,omitempty"`
	ContextLength              int64            `json:"ContextLength,omitempty"`
	MaxCompletionTokens        int64            `json:"MaxCompletionTokens,omitempty"`
	Thinking                   *thinkingSupport `json:"Thinking,omitempty"`
	UserDefined                bool             `json:"UserDefined,omitempty"`
}

type thinkingSupport struct {
	Min            int      `json:"Min"`
	Max            int      `json:"Max"`
	ZeroAllowed    bool     `json:"ZeroAllowed,omitempty"`
	DynamicAllowed bool     `json:"DynamicAllowed,omitempty"`
	Levels         []string `json:"Levels,omitempty"`
}

type httpRequest struct {
	HostCallbackID string              `json:"host_callback_id,omitempty"`
	Method         string              `json:"method"`
	URL            string              `json:"url"`
	Headers        map[string][]string `json:"headers,omitempty"`
	Body           []byte              `json:"body,omitempty"`
}

type httpResponse struct {
	StatusCode int                 `json:"StatusCode"`
	Headers    map[string][]string `json:"Headers"`
	Body       []byte              `json:"Body"`
}

type remoteResponse struct {
	Models map[string]remoteModel `json:"models"`
}

type remoteModel struct {
	DisplayName     string `json:"displayName"`
	MaxTokens       int64  `json:"maxTokens"`
	MaxOutputTokens int64  `json:"maxOutputTokens"`
}

type hostCaller func(method string, payload []byte) ([]byte, error)

var (
	configMu            sync.RWMutex
	configuredEndpoints = append([]string(nil), defaultEndpoints...)
)

func registration() map[string]any {
	return map[string]any{
		"schema_version": 1,
		"metadata": map[string]any{
			"Name":             pluginName,
			"Version":          "1.0.0",
			"Author":           "HunterWangwei",
			"GitHubRepository": "https://github.com/HunterWangwei/antigravity-model-sync",
			"ConfigFields": []map[string]any{
				{"Name": "endpoints", "Type": "array", "Description": "Optional ordered Antigravity API base URLs."},
			},
		},
		"capabilities": map[string]any{"model_provider": true},
	}
}

func handleMethod(method string, request []byte, caller hostCaller) ([]byte, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		var req lifecycleRequest
		if len(request) > 0 {
			if err := json.Unmarshal(request, &req); err != nil {
				return nil, fmt.Errorf("decode lifecycle request: %w", err)
			}
		}
		configMu.Lock()
		configuredEndpoints = parseEndpoints(req.ConfigYAML)
		configMu.Unlock()
		return okEnvelope(registration())
	case "model.static":
		models, err := loadStaticModels()
		if err != nil {
			return nil, err
		}
		return okEnvelope(modelResponse{Provider: provider, Models: models})
	case "model.for_auth":
		var req authModelRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, fmt.Errorf("decode auth model request: %w", err)
		}
		if !strings.EqualFold(strings.TrimSpace(req.AuthProvider), provider) {
			return okEnvelope(modelResponse{})
		}
		models, err := loadStaticModels()
		if err != nil {
			return nil, err
		}
		token := extractAccessToken(req.Metadata, req.StorageJSON)
		if token == "" || caller == nil {
			return okEnvelope(modelResponse{Provider: provider, Models: models})
		}
		configMu.RLock()
		endpoints := append([]string(nil), configuredEndpoints...)
		configMu.RUnlock()
		remote := fetchRemoteModels(caller, req.HostCallbackID, token, req.Metadata, req.Attributes, endpoints)
		return okEnvelope(modelResponse{Provider: provider, Models: mergeModels(models, remote)})
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func loadStaticModels() ([]modelInfo, error) {
	var models []modelInfo
	if err := json.Unmarshal(staticModelsJSON, &models); err != nil {
		return nil, fmt.Errorf("decode embedded models: %w", err)
	}
	return models, nil
}

func extractAccessToken(metadata map[string]any, storage []byte) string {
	if token := stringValue(metadata, "access_token"); token != "" {
		return token
	}
	var stored map[string]any
	if len(storage) == 0 || json.Unmarshal(storage, &stored) != nil {
		return ""
	}
	if token := stringValue(stored, "access_token"); token != "" {
		return token
	}
	if nested, ok := stored["token"].(map[string]any); ok {
		return stringValue(nested, "access_token")
	}
	return ""
}

func stringValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func fetchRemoteModels(caller hostCaller, callbackID, token string, metadata map[string]any, attributes map[string]string, endpoints []string) []modelInfo {
	if base := strings.TrimSpace(attributes["base_url"]); base != "" {
		endpoints = []string{base}
	} else if base := stringValue(metadata, "base_url"); base != "" {
		endpoints = []string{base}
	}
	body := []byte(`{}`)
	if project := stringValue(metadata, "project_id"); project != "" {
		body, _ = json.Marshal(map[string]string{"project": project})
	}
	for _, endpoint := range endpoints {
		req := httpRequest{
			HostCallbackID: callbackID,
			Method:         "POST",
			URL:            strings.TrimRight(endpoint, "/") + modelsPath,
			Headers: map[string][]string{
				"Authorization": {"Bearer " + token},
				"Content-Type":  {"application/json"},
			},
			Body: body,
		}
		payload, err := json.Marshal(req)
		if err != nil {
			continue
		}
		raw, err := caller("host.http.do", payload)
		if err != nil {
			continue
		}
		var env envelope
		if json.Unmarshal(raw, &env) != nil || !env.OK {
			continue
		}
		var response httpResponse
		if json.Unmarshal(env.Result, &response) != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
			continue
		}
		if models := parseRemoteModels(response.Body); len(models) > 0 {
			return models
		}
	}
	return nil
}

func parseRemoteModels(body []byte) []modelInfo {
	var response remoteResponse
	if json.Unmarshal(body, &response) != nil {
		return nil
	}
	models := make([]modelInfo, 0, len(response.Models))
	for id, remote := range response.Models {
		id = strings.TrimSpace(id)
		if id == "" || isInternalModel(id) {
			continue
		}
		displayName := strings.TrimSpace(remote.DisplayName)
		if displayName == "" {
			displayName = id
		}
		models = append(models, modelInfo{ID: id, Object: "model", OwnedBy: provider, Type: provider, DisplayName: displayName, Name: id, Description: displayName, ContextLength: remote.MaxTokens, MaxCompletionTokens: remote.MaxOutputTokens})
	}
	sort.Slice(models, func(i, j int) bool { return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID) })
	return models
}

func isInternalModel(id string) bool {
	switch strings.ToLower(id) {
	case "chat_20706", "chat_23310", "tab_flash_lite_preview", "tab_jump_flash_lite_preview", "gemini-2.5-flash-thinking", "gemini-2.5-pro":
		return true
	default:
		return false
	}
}

func mergeModels(static, remote []modelInfo) []modelInfo {
	result := append([]modelInfo(nil), static...)
	indices := make(map[string]int, len(result))
	for i := range result {
		indices[strings.ToLower(strings.TrimSpace(result[i].ID))] = i
	}
	for _, candidate := range remote {
		key := strings.ToLower(strings.TrimSpace(candidate.ID))
		if index, exists := indices[key]; exists {
			fillMissingModelFields(&result[index], candidate)
			continue
		}
		indices[key] = len(result)
		result = append(result, candidate)
	}
	return result
}

func fillMissingModelFields(dst *modelInfo, src modelInfo) {
	if dst.DisplayName == "" {
		dst.DisplayName = src.DisplayName
	}
	if dst.Name == "" {
		dst.Name = src.Name
	}
	if dst.Description == "" {
		dst.Description = src.Description
	}
	if dst.ContextLength == 0 {
		dst.ContextLength = src.ContextLength
	}
	if dst.MaxCompletionTokens == 0 {
		dst.MaxCompletionTokens = src.MaxCompletionTokens
	}
}

func parseEndpoints(config []byte) []string {
	endpoints := make([]string, 0, 2)
	inEndpoints := false
	for _, line := range strings.Split(string(config), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "endpoints:") {
			inEndpoints = true
			continue
		}
		if !inEndpoints {
			continue
		}
		if !strings.HasPrefix(trimmed, "-") {
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				break
			}
			continue
		}
		endpoint := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "-")), "\"'")
		if strings.HasPrefix(endpoint, "https://") || strings.HasPrefix(endpoint, "http://") {
			endpoints = append(endpoints, strings.TrimRight(endpoint, "/"))
		}
	}
	if len(endpoints) == 0 {
		return append([]string(nil), defaultEndpoints...)
	}
	return endpoints
}

func okEnvelope(result any) ([]byte, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

var errHostCall = errors.New("host callback failed")
