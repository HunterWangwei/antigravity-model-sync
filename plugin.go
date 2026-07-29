package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
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
	Routes    []managementRoute
	Resources []resourceRoute
}

type resourceRoute struct {
	Path        string
	Menu        string
	Description string
}

type managementResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}

type managementRequest struct {
	Method         string
	Path           string
	Query          map[string][]string
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type hostAuthListResponse struct {
	Files []hostAuthFileEntry `json:"files"`
}

type hostAuthFileEntry struct {
	ID          string `json:"id"`
	AuthIndex   string `json:"auth_index"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Provider    string `json:"provider"`
	Disabled    bool   `json:"disabled"`
	Unavailable bool   `json:"unavailable"`
}

type hostAuthGetResponse struct {
	JSON json.RawMessage `json:"json"`
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
			"Version":          "1.0.5",
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
		return okEnvelope(managementRegistration{
			Routes: []managementRoute{{
				Method:      "GET",
				Path:        "/plugins/antigravity-model-sync/status",
				Description: "查看 Antigravity 动态模型同步状态。",
			}},
			Resources: []resourceRoute{{
				Path:        "/status",
				Menu:        "Antigravity 模型同步",
				Description: "查看各账号的动态模型同步结果。",
			}},
		})
	case "management.handle":
		var req managementRequest
		if len(request) > 0 {
			if err := json.Unmarshal(request, &req); err != nil {
				return nil, fmt.Errorf("decode management request: %w", err)
			}
		}
		if strings.Contains(req.Path, "/v0/resource/plugins/") && queryEnabled(req.Query, "refresh") {
			refreshAccountStatuses(caller, req.HostCallbackID)
		}
		status := currentStatus()
		if strings.Contains(req.Path, "/v0/resource/plugins/") {
			return okEnvelope(managementResponse{
				StatusCode: 200,
				Headers:    map[string][]string{"Content-Type": {"text/html; charset=utf-8"}},
				Body:       buildStatusHTML(status),
			})
		}
		body, err := json.Marshal(status)
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

func queryEnabled(query map[string][]string, key string) bool {
	for _, value := range query[key] {
		if value == "1" || strings.EqualFold(value, "true") {
			return true
		}
	}
	return false
}

func refreshAccountStatuses(caller hostCaller, callbackID string) {
	if caller == nil {
		return
	}
	var listed hostAuthListResponse
	if err := callHostJSON(caller, "host.auth.list", callbackID, map[string]any{}, &listed); err != nil {
		return
	}
	statuses := make(map[string]syncStatus)
	configMu.RLock()
	endpoints := append([]string(nil), configuredEndpoints...)
	configMu.RUnlock()
	for _, entry := range listed.Files {
		if !strings.EqualFold(strings.TrimSpace(entry.Provider), provider) && !strings.EqualFold(strings.TrimSpace(entry.Type), provider) {
			continue
		}
		authID := strings.TrimSpace(entry.ID)
		if authID == "" {
			authID = strings.TrimSpace(entry.AuthIndex)
		}
		if authID == "" {
			authID = strings.TrimSpace(entry.Name)
		}
		if entry.Disabled || entry.Unavailable {
			statuses[authID] = syncStatus{AuthID: authID, AttemptedAt: nowUTC(), Message: "账号已禁用或当前不可用，未执行同步。"}
			continue
		}
		var auth hostAuthGetResponse
		if err := callHostJSON(caller, "host.auth.get", callbackID, map[string]string{"auth_index": entry.AuthIndex}, &auth); err != nil {
			statuses[authID] = syncStatus{AuthID: authID, AttemptedAt: nowUTC(), Message: "无法读取账号凭据，未执行同步。"}
			continue
		}
		var metadata map[string]any
		_ = json.Unmarshal(auth.JSON, &metadata)
		token := extractAccessToken(metadata, auth.JSON)
		if token == "" {
			statuses[authID] = syncStatus{AuthID: authID, AttemptedAt: nowUTC(), Message: "未找到 access_token，未执行同步。"}
			continue
		}
		remote := fetchRemoteModels(caller, authID, callbackID, token, metadata, nil, endpoints)
		models, err := loadStaticModels()
		if err == nil {
			updateMergedModelCount(authID, len(mergeModels(models, remote)))
		}
		statusMu.RLock()
		status, ok := accountStatuses[authID]
		statusMu.RUnlock()
		if ok {
			statuses[authID] = status
		}
	}
	statusMu.Lock()
	accountStatuses = statuses
	statusMu.Unlock()
}

func callHostJSON(caller hostCaller, method, callbackID string, request any, response any) error {
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if callbackID != "" {
		var fields map[string]any
		if err = json.Unmarshal(payload, &fields); err != nil {
			return err
		}
		fields["host_callback_id"] = callbackID
		payload, err = json.Marshal(fields)
		if err != nil {
			return err
		}
	}
	raw, err := caller(method, payload)
	if err != nil {
		return err
	}
	var env envelope
	if err = json.Unmarshal(raw, &env); err != nil {
		return err
	}
	if !env.OK {
		return errors.New("host callback failed")
	}
	return json.Unmarshal(env.Result, response)
}

func currentStatus() statusResponse {
	statusMu.RLock()
	accounts := make([]syncStatus, 0, len(accountStatuses))
	for _, status := range accountStatuses {
		accounts = append(accounts, status)
	}
	statusMu.RUnlock()
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].AuthID < accounts[j].AuthID })
	return statusResponse{PluginVersion: "1.0.5", UserAgent: userAgent, Accounts: accounts}
}

func buildStatusHTML(status statusResponse) []byte {
	var rows strings.Builder
	if len(status.Accounts) == 0 {
		rows.WriteString(`<div class="empty">暂无同步记录。请确认已配置 Antigravity 账号，然后重启 CPA。</div>`)
	}
	for _, account := range status.Accounts {
		stateClass := "failed"
		stateText := "未成功"
		if account.Success {
			stateClass = "success"
			stateText = "同步成功"
		}
		fmt.Fprintf(&rows, `<article class="card"><div class="card-head"><h2>%s</h2><span class="badge %s">%s</span></div><dl><dt>最后同步</dt><dd>%s</dd><dt>请求端点</dt><dd class="mono">%s</dd><dt>HTTP 状态</dt><dd>%d</dd><dt>远程模型</dt><dd>%d</dd><dt>合并后模型</dt><dd>%d</dd></dl><p class="message">%s</p></article>`,
			html.EscapeString(account.AuthID), stateClass, stateText,
			html.EscapeString(account.AttemptedAt), html.EscapeString(account.Endpoint), account.HTTPStatus,
			account.RemoteModelCount, account.MergedModelCount, html.EscapeString(account.Message))
	}
	page := fmt.Sprintf(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Antigravity 模型同步</title><style>:root{color-scheme:light dark;font-family:Inter,"Segoe UI","Microsoft YaHei",sans-serif}body{margin:0;background:#0b1020;color:#e8edf7}.wrap{max-width:1100px;margin:auto;padding:32px 20px}.hero{padding:28px;border:1px solid #263453;border-radius:18px;background:linear-gradient(135deg,#111a31,#172442)}.hero-head{display:flex;align-items:flex-start;justify-content:space-between;gap:18px;flex-wrap:wrap}h1{margin:0 0 8px;font-size:28px}.sub{color:#aebbd2;margin:0}.refresh{border:1px solid #5079c8;border-radius:10px;background:#2b5db5;color:#fff;padding:10px 16px;font:inherit;font-weight:600;cursor:pointer;text-decoration:none}.refresh:hover{background:#3670d2}.meta{display:flex;gap:12px;flex-wrap:wrap;margin-top:18px}.pill{padding:7px 11px;border-radius:999px;background:#243353;color:#dbe7ff;font-size:13px}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(310px,1fr));gap:16px;margin-top:20px}.card{border:1px solid #263453;border-radius:16px;background:#111a2d;padding:20px}.card-head{display:flex;align-items:center;justify-content:space-between;gap:12px}.card h2{font-size:16px;margin:0;overflow-wrap:anywhere}.badge{font-size:12px;padding:5px 9px;border-radius:999px;white-space:nowrap}.success{background:#123d30;color:#75e2b5}.failed{background:#4a2529;color:#ffadb5}dl{display:grid;grid-template-columns:105px 1fr;gap:10px;margin:20px 0}dt{color:#8fa0bb}dd{margin:0;overflow-wrap:anywhere}.mono{font-family:ui-monospace,SFMono-Regular,Consolas,monospace;font-size:12px}.message{margin:0;padding:12px;border-radius:10px;background:#0b1325;color:#c7d3e8}.empty{margin-top:20px;padding:24px;border:1px dashed #405170;border-radius:14px;color:#aebbd2}</style></head><body><main class="wrap"><section class="hero"><div class="hero-head"><div><h1>Antigravity 动态模型同步</h1><p class="sub">查看插件的最近同步结果。页面不会显示 Access Token。</p></div><a class="refresh" href="?refresh=1">手动刷新</a></div><div class="meta"><span class="pill">插件版本 %s</span><span class="pill">User-Agent: %s</span><span class="pill">账号数 %d</span></div></section><section class="grid">%s</section></main></body></html>`,
		html.EscapeString(status.PluginVersion), html.EscapeString(status.UserAgent), len(status.Accounts), rows.String())
	return []byte(page)
}

func parseRemoteModels(body []byte) []modelInfo {
	var response remoteResponse
	if json.Unmarshal(body, &response) != nil {
		return nil
	}
	models := make([]modelInfo, 0, len(response.Models))
	for id, remote := range response.Models {
		id = strings.TrimSpace(id)
		if id == "" {
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
