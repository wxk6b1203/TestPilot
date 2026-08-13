// Package impexp 提供 OpenAPI / curl 的导入导出（MVP：HTTP 接口维度）。
package impexp

import (
	"encoding/json"
	"fmt"
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

// ImportOpenAPI 解析 OpenAPI 3.x 文档（JSON 或 YAML），导入全部 path 操作为 HttpApi。
func ImportOpenAPI(db *gorm.DB, tenantID, projectID int64, doc []byte) (*ImportResult, error) {
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
	res := &ImportResult{APIIDs: []int64{}}
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
			body := openapiBody(op["requestBody"])
			created, id, err := insertAPI(db, tenantID, projectID, m, path, headers, params, body)
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
