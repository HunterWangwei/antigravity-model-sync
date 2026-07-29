package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	pluginName = "antigravity-model-sync"
	provider   = "antigravity"
	modelsPath = "/v1internal:fetchAvailableModels"
	userAgent  = "antigravity/hub/2.2.1 darwin/arm64"
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

type managementRoute struct {
	Method      string
	Path        string
	Menu        string
	Description string
}

type managementRegistration struct {
	Routes []managementRoute
}

type managementResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}

type syncStatus struct {
	AuthID           string `json:"auth_id"`
	AttemptedAt      string `json:"attempted_at"`
	Endpoint         string `json:"endpoint,omitempty"`
	HTTPStatus       int    `json:"http_status,omitempty"`
	RemoteModelCount int    `json:"remote_model_count"`
	MergedModelCount int    `json:"merged_model_count"`
	Success          bool   `json:"success"`
	Message          string `json:"message"`
}

type statusResponse struct {
	PluginVersion string       `json:"plugin_version"`
	UserAgent     string       `json:"user_agent"`
	Accounts      []syncStatus `json:"accounts"`
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
	statusMu            sync.RWMutex
	accountStatuses     = make(map[string]syncStatus)
)

func registration() map[string]any {
	return map[string]any{
		"schema_version": 1,
		"metadata": map[string]any{
			"Name":             "Antigravity 动态模型同步",
			"Version":          "1.0.2",
			"Author":           "HunterWangwei",
			"GitHubRepository": "https://github.com/HunterWangwei/antigravity-model-sync",
			"ConfigFields": []map[string]any{
				{"Name": "endpoints", "Type": "array", "Description": "可选，按顺序尝试的 Antigravity API 基础地址列表。"},
			},
		},
		"capabilities": map[string]any{"model_provider": true, "management_api": true},
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
		if token == "" {
			recordSyncStatus(syncStatus{AuthID: req.AuthID, AttemptedAt: nowUTC(), MergedModelCount: len(models), Message: "未找到 access_token，已使用官方静态模型。"})
			return okEnvelope(modelResponse{Provider: provider, Models: models})
		}
		if caller == nil {
			recordSyncStatus(syncStatus{AuthID: req.AuthID, AttemptedAt: nowUTC(), MergedModelCount: len(models), Message: "宿主 HTTP 回调不可用，已使用官方静态模型。"})
			return okEnvelope(modelResponse{Provider: provider, Models: models})
		}
		configMu.RLock()
		endpoints := append([]string(nil), configuredEndpoints...)
		configMu.RUnlock()
		remote := fetchRemoteModels(caller, req.AuthID, req.HostCallbackID, token, req.Metadata, req.Attributes, endpoints)
		merged := mergeModels(models, remote)
		updateMergedModelCount(req.AuthID, len(merged))
		return okEnvelope(modelResponse{Provider: provider, Models: merged})
	case "management.register":
		return okEnvelope(managementRegistration{Routes: []managementRoute{{
			Method:      "GET",
			Path:        "/plugins/antigravity-model-sync/status",
			Description: "查看 Antigravity 动态模型同步状态。",
		}}})
	case "management.handle":
		body, err := json.Marshal(currentStatus())
		if err != nil {
			return nil, fmt.Errorf("encode sync status: %w", err)
		}
		return okEnvelope(managementResponse{
			StatusCode: 200,
			Headers:    map[string][]string{"Content-Type": {"application/json; charset=utf-8"}},
			Body:       body,
		})
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

func fetchRemoteModels(caller hostCaller, authID, callbackID, token string, metadata map[string]any, attributes map[string]string, endpoints []string) []modelInfo {
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
		endpoint = strings.TrimRight(endpoint, "/")
		status := syncStatus{AuthID: authID, AttemptedAt: nowUTC(), Endpoint: endpoint, Message: "正在同步。"}
		req := httpRequest{
			HostCallbackID: callbackID,
			Method:         "POST",
			URL:            endpoint + modelsPath,
			Headers: map[string][]string{
				"Authorization": {"Bearer " + token},
				"Content-Type":  {"application/json"},
				"User-Agent":    {userAgent},
			},
			Body: body,
		}
		payload, err := json.Marshal(req)
		if err != nil {
			status.Message = "构建请求失败。"
			recordSyncStatus(status)
			continue
		}
		raw, err := caller("host.http.do", payload)
		if err != nil {
			status.Message = "宿主 HTTP 请求失败。"
			recordSyncStatus(status)
			continue
		}
		var env envelope
		if json.Unmarshal(raw, &env) != nil {
			status.Message = "无法解析宿主 HTTP 响应。"
			recordSyncStatus(status)
			continue
		}
		if !env.OK {
			status.Message = "宿主 HTTP 回调返回错误。"
			recordSyncStatus(status)
			continue
		}
		var response httpResponse
		if json.Unmarshal(env.Result, &response) != nil {
			status.Message = "无法解析 Antigravity HTTP 响应。"
			recordSyncStatus(status)
			continue
		}
		status.HTTPStatus = response.StatusCode
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			status.Message = fmt.Sprintf("Antigravity 接口返回 HTTP %d。", response.StatusCode)
			recordSyncStatus(status)
			continue
		}
		if models := parseRemoteModels(response.Body); len(models) > 0 {
			status.Success = true
			status.RemoteModelCount = len(models)
			status.Message = "远程模型同步成功。"
			recordSyncStatus(status)
			return models
		}
		status.Message = "Antigravity 接口未返回可用模型。"
		recordSyncStatus(status)
	}
	return nil
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func recordSyncStatus(status syncStatus) {
	key := strings.TrimSpace(status.AuthID)
	if key == "" {
		key = "unknown"
	}
	status.AuthID = key
	statusMu.Lock()
	accountStatuses[key] = status
	statusMu.Unlock()
}

func updateMergedModelCount(authID string, count int) {
	key := strings.TrimSpace(authID)
	if key == "" {
		key = "unknown"
	}
	statusMu.Lock()
	status := accountStatuses[key]
	status.AuthID = key
	status.MergedModelCount = count
	accountStatuses[key] = status
	statusMu.Unlock()
}

func currentStatus() statusResponse {
	statusMu.RLock()
	accounts := make([]syncStatus, 0, len(accountStatuses))
	for _, status := range accountStatuses {
		accounts = append(accounts, status)
	}
	statusMu.RUnlock()
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].AuthID < accounts[j].AuthID })
	return statusResponse{PluginVersion: "1.0.2", UserAgent: userAgent, Accounts: accounts}
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
