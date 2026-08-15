// Package impexp 提供 OpenAPI / curl 的导入导出（MVP：HTTP 接口维度）。
package impexp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/model"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

// ---- 通用 ----

var methodToEnum = map[string]int16{
	"get": 1, "post": 2, "put": 3, "delete": 4, "patch": 5, "head": 6, "options": 7,
}

func kvJSON(pairs [][2]string) model.JSON {
	if len(pairs) == 0 {
		return nil
	}
	arr := make([]map[string]string, 0, len(pairs))
	for _, p := range pairs {
		arr = append(arr, map[string]string{"key": p[0], "value": p[1]})
	}
	b, _ := json.Marshal(arr)
	return model.JSON(b)
}

func bodyJSON(contentType commonv1.BodyContentType, raw string) model.JSON {
	b, _ := json.Marshal(map[string]any{
		"contentType": contentType,
		"raw":         raw,
	})
	return model.JSON(b)
}

// insertAPI 幂等插入（同项目 + method + uri 已存在则跳过）。
func insertAPI(db *gorm.DB, tenantID, projectID int64, method int16, uri string,
	headers, params, body model.JSON) (created bool, id int64, err error) {
	var exist model.HttpApi
	q := db.Where("tenant_id = ? AND project_id = ? AND method = ? AND uri = ?",
		tenantID, projectID, method, uri).Limit(1).Find(&exist)
	if q.Error != nil {
		return false, 0, q.Error
	}
	if exist.ID != 0 {
		return false, exist.ID, nil
	}
	api := model.HttpApi{
		ID: model.NextID(), TenantID: tenantID, ProjectID: projectID,
		Method: method, URI: uri, Headers: headers, Params: params, Body: body,
	}
	if err := db.Create(&api).Error; err != nil {
		return false, 0, err
	}
	return true, api.ID, nil
}

// ImportResult 汇总导入结果。
type ImportResult struct {
	Created int     `json:"created"`
	Skipped int     `json:"skipped"`
	APIIDs  []int64 `json:"api_ids"`
}

// ---- OpenAPI 导入 ----

// specAPI 解析出的单个 path 操作。
type specAPI struct {
	method          int16
	uri             string
	headers, params model.JSON
	body            model.JSON
}

// parseSpecAPIs 解析 OpenAPI 3.x 文档（JSON 或 YAML）为接口条目列表。
func parseSpecAPIs(doc []byte) ([]specAPI, error) {
	var root map[string]any
	if err := json.Unmarshal(doc, &root); err != nil {
		// 尝试 YAML（YAML 是 JSON 超集方向相反，故分开解析）
		var y any
		if yerr := yaml.Unmarshal(doc, &y); yerr != nil {
			return nil, fmt.Errorf("document is neither valid JSON nor YAML")
		}
		root = toStringKeyMap(y)
	}
	paths, ok := root["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		return nil, fmt.Errorf("no paths found in document")
	}
	var out []specAPI
	for path, itemRaw := range paths {
		item, ok := itemRaw.(map[string]any)
		if !ok {
			continue
		}
		sharedParams, _ := item["parameters"].([]any)
		for method, opRaw := range item {
			m, ok := methodToEnum[strings.ToLower(method)]
			if !ok {
				continue
			}
			op, _ := opRaw.(map[string]any)
			params, headers := openapiParams(append(sharedParams, asList(op["parameters"])...))
			out = append(out, specAPI{
				method:  m,
				uri:     path,
				headers: headers,
				params:  params,
				body:    openapiBody(op["requestBody"]),
			})
		}
	}
	return out, nil
}

// ImportOpenAPI 解析 OpenAPI 3.x 文档（JSON 或 YAML），导入全部 path 操作为 HttpApi。
func ImportOpenAPI(db *gorm.DB, tenantID, projectID int64, doc []byte) (*ImportResult, error) {
	if err := ensureProject(db, tenantID, projectID); err != nil {
		return nil, err
	}
	entries, err := parseSpecAPIs(doc)
	if err != nil {
		return nil, err
	}
	res := &ImportResult{APIIDs: []int64{}}
	for _, sa := range entries {
		created, id, err := insertAPI(db, tenantID, projectID, sa.method, sa.uri, sa.headers, sa.params, sa.body)
		if err != nil {
			return res, err
		}
		if created {
			res.Created++
			res.APIIDs = append(res.APIIDs, id)
		} else {
			res.Skipped++
		}
	}
	return res, nil
}

func asList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

func openapiParams(list []any) (query, headers model.JSON) {
	var qp, hp [][2]string
	for _, pRaw := range list {
		p, ok := pRaw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := p["name"].(string)
		in, _ := p["in"].(string)
		if name == "" {
			continue
		}
		switch in {
		case "query":
			qp = append(qp, [2]string{name, ""})
		case "header":
			hp = append(hp, [2]string{name, ""})
		}
	}
	return kvJSON(qp), kvJSON(hp)
}

func openapiBody(rbRaw any) model.JSON {
	rb, ok := rbRaw.(map[string]any)
	if !ok {
		return nil
	}
	content, ok := rb["content"].(map[string]any)
	if !ok {
		return nil
	}
	for mt, mediaRaw := range content {
		media, _ := mediaRaw.(map[string]any)
		switch {
		case strings.Contains(mt, "json"):
			if ex := media["example"]; ex != nil {
				b, _ := json.Marshal(ex)
				return bodyJSON(commonv1.BodyContentType_BODY_CONTENT_TYPE_JSON, string(b))
			}
			if schema, ok := media["schema"].(map[string]any); ok {
				return bodyJSON(commonv1.BodyContentType_BODY_CONTENT_TYPE_JSON,
					string(schemaSkeleton(schema)))
			}
			return bodyJSON(commonv1.BodyContentType_BODY_CONTENT_TYPE_JSON, "{}")
		case strings.Contains(mt, "x-www-form-urlencoded"):
			return bodyJSON(commonv1.BodyContentType_BODY_CONTENT_TYPE_X_WWW_FORM_URLENCODED, "")
		}
	}
	return nil
}

// schemaSkeleton 从 JSON Schema 生成最小占位示例（一层）。
func schemaSkeleton(schema map[string]any) []byte {
	out := map[string]any{}
	props, _ := schema["properties"].(map[string]any)
	for name, pRaw := range props {
		p, _ := pRaw.(map[string]any)
		switch p["type"] {
		case "integer", "number":
			out[name] = 0
		case "boolean":
			out[name] = false
		case "array":
			out[name] = []any{}
		case "object":
			out[name] = map[string]any{}
		default:
			out[name] = ""
		}
	}
	b, _ := json.Marshal(out)
	return b
}

// toStringKeyMap 把 yaml.v3 的 map[any]any 递归转成 map[string]any。
func toStringKeyMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

// ---- OpenAPI 导出 ----

// ExportOpenAPI 把项目下全部 HttpApi 导出为 OpenAPI 3.0 文档（JSON）。
func ExportOpenAPI(db *gorm.DB, tenantID, projectID int64, title string) ([]byte, error) {
	var apis []model.HttpApi
	if err := db.Where("tenant_id = ? AND project_id = ?", tenantID, projectID).
		Order("uri, method").Find(&apis).Error; err != nil {
		return nil, err
	}
	paths := map[string]any{}
	for _, a := range apis {
		methodName := strings.TrimPrefix(
			strings.ToLower(commonv1.HttpMethod_name[int32(a.Method)]), "http_method_")
		if methodName == "" || methodName == "unspecified" {
			continue
		}
		item, _ := paths[a.URI].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[a.URI] = item
		}
		op := map[string]any{
			"summary":   fmt.Sprintf("%s %s", methodName, a.URI),
			"responses": map[string]any{"200": map[string]any{"description": "OK"}},
		}
		var params []any
		for _, kv := range unmarshalKV(a.Params) {
			params = append(params, map[string]any{
				"name": kv[0], "in": "query", "schema": map[string]any{"type": "string"}})
		}
		for _, kv := range unmarshalKV(a.Headers) {
			params = append(params, map[string]any{
				"name": kv[0], "in": "header", "schema": map[string]any{"type": "string"}})
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		if raw := unmarshalBodyRaw(a.Body); raw != "" {
			op["requestBody"] = map[string]any{"content": map[string]any{
				"application/json": map[string]any{"example": raw}}}
		}
		item[methodName] = op
	}
	doc := map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": title, "version": "1.0.0"},
		"paths":   paths,
	}
	return json.MarshalIndent(doc, "", "  ")
}

func unmarshalKV(raw model.JSON) [][2]string {
	var arr []map[string]string
	if len(raw) == 0 || json.Unmarshal([]byte(raw), &arr) != nil {
		return nil
	}
	out := make([][2]string, 0, len(arr))
	for _, kv := range arr {
		out = append(out, [2]string{kv["key"], kv["value"]})
	}
	return out
}

func unmarshalBodyRaw(raw model.JSON) string {
	if len(raw) == 0 {
		return ""
	}
	var body map[string]any
	if json.Unmarshal([]byte(raw), &body) != nil {
		return ""
	}
	s, _ := body["raw"].(string)
	return s
}

// ---- OpenAPI diff（ApplyOpenApiDiff：按 method+uri 匹配增量应用）----

// DiffEntry 单个接口的差异结论。
type DiffEntry struct {
	APIID   int64  `json:"api_id"`
	Kind    string `json:"kind"` // added | removed | changed | breaking
	Summary string `json:"summary"`
}

// DiffResult ApplyOpenAPIDiff 结果。UpdatedAPIIDs 含新增与更新；
// UpdatedCaseIDs 仅 autoUpdateCases=true 时非空（被重写 inline 的用例）。
type DiffResult struct {
	Diffs          []DiffEntry `json:"diffs"`
	UpdatedAPIIDs  []int64     `json:"updated_api_ids"`
	UpdatedCaseIDs []int64     `json:"updated_case_ids"`
}

// apiKey method+uri 为匹配键。
func apiKey(method int16, uri string) string {
	return strconv.Itoa(int(method)) + "|" + uri
}

// ApplyOpenAPIDiff 将新 spec 增量应用到项目：新增→创建；变更→更新；缺失→仅报告不删除
// （删除属破坏性操作，留给用户在控制台显式执行）。
// kind=breaking 判定：已有 query/header 参数键被移除，或 body content_type 变化。
// autoUpdateCases=true 时，引用变更接口（api_id 步）的声明式用例在派发前由
// runner 解析为内联快照，这里把新 spec 的 inline 直接写回用例 definition，
// 使控制台展示与运行行为一致。
func ApplyOpenAPIDiff(db *gorm.DB, tenantID, projectID int64, doc []byte, autoUpdateCases bool) (*DiffResult, error) {
	if err := ensureProject(db, tenantID, projectID); err != nil {
		return nil, err
	}
	entries, err := parseSpecAPIs(doc)
	if err != nil {
		return nil, err
	}
	var apis []model.HttpApi
	if err := db.Where("tenant_id = ? AND project_id = ?", tenantID, projectID).Find(&apis).Error; err != nil {
		return nil, err
	}
	existing := make(map[string]model.HttpApi, len(apis))
	for _, a := range apis {
		existing[apiKey(a.Method, a.URI)] = a
	}
	res := &DiffResult{Diffs: []DiffEntry{}, UpdatedAPIIDs: []int64{}, UpdatedCaseIDs: []int64{}}

	for _, sa := range entries {
		key := apiKey(sa.method, sa.uri)
		exist, ok := existing[key]
		if !ok {
			_, id, err := insertAPI(db, tenantID, projectID, sa.method, sa.uri, sa.headers, sa.params, sa.body)
			if err != nil {
				return res, err
			}
			res.Diffs = append(res.Diffs, DiffEntry{APIID: id, Kind: "added", Summary: "created from spec"})
			res.UpdatedAPIIDs = append(res.UpdatedAPIIDs, id)
			continue
		}
		delete(existing, key)
		if jsonEqual(exist.Params, sa.params) && jsonEqual(exist.Headers, sa.headers) &&
			jsonEqual(exist.Body, sa.body) {
			continue // 未变
		}
		kind, summary := "changed", diffSummary(&exist, sa)
		if diffBreaking(&exist, sa) {
			kind = "breaking"
		}
		if err := db.Model(&exist).Updates(map[string]any{
			"params": sa.params, "headers": sa.headers, "body": sa.body,
		}).Error; err != nil {
			return res, err
		}
		res.Diffs = append(res.Diffs, DiffEntry{APIID: exist.ID, Kind: kind, Summary: summary})
		res.UpdatedAPIIDs = append(res.UpdatedAPIIDs, exist.ID)
		if autoUpdateCases {
			caseIDs, err := updateCasesInline(db, tenantID, projectID, exist.ID, sa)
			if err != nil {
				return res, err
			}
			res.UpdatedCaseIDs = append(res.UpdatedCaseIDs, caseIDs...)
		}
	}
	for _, a := range existing {
		res.Diffs = append(res.Diffs, DiffEntry{
			APIID:   a.ID,
			Kind:    "removed",
			Summary: "not present in new spec (kept; delete manually if intended)",
		})
	}
	return res, nil
}

// jsonEqual 结构相等比较（nil/空视为相等；解析失败退化为字节比较）。
func jsonEqual(a, b model.JSON) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return bytes.Equal(a, b)
	}
	return reflect.DeepEqual(x, y)
}

// kvKeys 提取 params/headers JSON 列中的键集合。
func kvKeys(raw model.JSON) map[string]bool {
	out := map[string]bool{}
	var arr []map[string]string
	if json.Unmarshal([]byte(raw), &arr) != nil {
		return out
	}
	for _, kv := range arr {
		out[kv["key"]] = true
	}
	return out
}

// bodyContentType 读取 body JSON 列 {"contentType": N}。
func bodyContentType(raw model.JSON) int64 {
	if len(raw) == 0 {
		return 0
	}
	var b map[string]any
	if json.Unmarshal([]byte(raw), &b) != nil {
		return 0
	}
	switch v := b["contentType"].(type) {
	case float64:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	return 0
}

// diffBreaking 判定破坏性变更：参数键被移除 / body content_type 变化。
func diffBreaking(exist *model.HttpApi, sa specAPI) bool {
	oldKeys, newKeys := kvKeys(exist.Params), kvKeys(sa.params)
	for k := range oldKeys {
		if !newKeys[k] {
			return true
		}
	}
	oldKeys, newKeys = kvKeys(exist.Headers), kvKeys(sa.headers)
	for k := range oldKeys {
		if !newKeys[k] {
			return true
		}
	}
	oldCT, newCT := bodyContentType(exist.Body), bodyContentType(sa.body)
	return oldCT != 0 && newCT != oldCT
}

// diffSummary 人类可读的变更描述。
func diffSummary(exist *model.HttpApi, sa specAPI) string {
	var parts []string
	if !jsonEqual(exist.Params, sa.params) {
		parts = append(parts, "params changed")
	}
	if !jsonEqual(exist.Headers, sa.headers) {
		parts = append(parts, "headers changed")
	}
	if !jsonEqual(exist.Body, sa.body) {
		parts = append(parts, "body changed")
	}
	if diffBreaking(exist, sa) {
		parts = append(parts, "breaking: removed param key or body content_type changed")
	}
	if len(parts) == 0 {
		return "no effective change"
	}
	return strings.Join(parts, "; ")
}

// inlineAPI 生成 api_call 步骤的 inline 快照（protojson 兼容 HttpApi）。
func inlineAPI(sa specAPI, apiID int64) map[string]any {
	m := map[string]any{
		"id":     strconv.FormatInt(apiID, 10),
		"method": commonv1.HttpMethod_name[int32(sa.method)],
		"uri":    sa.uri,
	}
	if len(sa.params) > 0 {
		m["params"] = rawJSONValue(sa.params)
	}
	if len(sa.headers) > 0 {
		m["headers"] = rawJSONValue(sa.headers)
	}
	if len(sa.body) > 0 {
		m["body"] = rawJSONValue(sa.body)
	}
	return m
}

func rawJSONValue(raw model.JSON) any {
	var v any
	if json.Unmarshal([]byte(raw), &v) == nil {
		return v
	}
	return nil
}

// asStepList 把 JSON 步骤数组转为 map 列表。
func asStepList(v any) []map[string]any {
	arr, _ := v.([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, x := range arr {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// walkStepCalls 递归遍历步骤树（api_call 自身 + if/loop/retry 嵌套），
// 对 api_id 命中 want 的 api_call 调用 fn。
func walkStepCalls(node map[string]any, want string, fn func(map[string]any)) {
	if call, ok := node["api_call"].(map[string]any); ok {
		if id, _ := call["api_id"].(string); id == want {
			fn(call)
		}
	}
	for _, k := range []string{"if_step", "loop_step", "retry_step"} {
		if sub, ok := node[k].(map[string]any); ok {
			walkStepCalls(sub, want, fn)
		}
	}
	for _, k := range []string{"then_steps", "else_steps", "body_steps"} {
		for _, s := range asStepList(node[k]) {
			walkStepCalls(s, want, fn)
		}
	}
	if b, ok := node["body_step"].(map[string]any); ok {
		walkStepCalls(b, want, fn)
	}
}

// updateCasesInline 为引用 apiID 的声明式用例把新 spec 快照写回 definition 的
// inline 字段（保留 api_id 引用与 override），返回被更新的用例 ID。
func updateCasesInline(db *gorm.DB, tenantID, projectID, apiID int64, sa specAPI) ([]int64, error) {
	var cases []model.TestCase
	if err := db.Where("tenant_id = ? AND project_id = ? AND type = ?",
		tenantID, projectID, int16(commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE)).Find(&cases).Error; err != nil {
		return nil, err
	}
	want := strconv.FormatInt(apiID, 10)
	inline := inlineAPI(sa, apiID)
	var updated []int64
	for _, tc := range cases {
		var def map[string]any
		if json.Unmarshal([]byte(tc.Definition), &def) != nil {
			continue
		}
		touched := false
		for _, s := range asStepList(def["steps"]) {
			walkStepCalls(s, want, func(call map[string]any) {
				call["inline"] = inline
				touched = true
			})
		}
		if !touched {
			continue
		}
		b, err := json.Marshal(def)
		if err != nil {
			continue
		}
		if err := db.Model(&tc).Update("definition", model.JSON(b)).Error; err != nil {
			return updated, err
		}
		updated = append(updated, tc.ID)
	}
	return updated, nil
}
