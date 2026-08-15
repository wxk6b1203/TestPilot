package impexp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// ---- Postman Collection v2.1 导入导出 ----

const postmanSchema = "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"

// ImportPostman 解析 Postman Collection v2.1（folder/item 递归），把 request 条目
// 导入为 HttpApi（method+uri 幂等，与 OpenAPI 导入同语义）。URL 取 raw（剥协议与 host），
// query 拆入 Params；headers 落 Headers；raw body 落 Body（JSON 探测）。
func ImportPostman(db *gorm.DB, tenantID, projectID int64, doc []byte) (*ImportResult, error) {
	if err := ensureProject(db, tenantID, projectID); err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(doc, &root); err != nil {
		return nil, fmt.Errorf("invalid postman collection json: %v", err)
	}
	if _, ok := root["item"]; !ok {
		return nil, fmt.Errorf("not a postman collection (missing item)")
	}
	res := &ImportResult{APIIDs: []int64{}}
	var walk func(item map[string]any) error
	walk = func(item map[string]any) error {
		req, _ := item["request"].(map[string]any)
		if req != nil {
			m, uri, params, headers, body, err := parsePostmanRequest(req)
			if err != nil {
				return err
			}
			created, id, err := insertAPI(db, tenantID, projectID, m, uri, headers, params, body)
			if err != nil {
				return err
			}
			if created {
				res.Created++
				res.APIIDs = append(res.APIIDs, id)
			} else {
				res.Skipped++
			}
		}
		for _, sub := range asStepList(item["item"]) {
			if err := walk(sub); err != nil {
				return err
			}
		}
		return nil
	}
	for _, top := range asStepList(root["item"]) {
		if err := walk(top); err != nil {
			return res, err
		}
	}
	return res, nil
}

// parsePostmanRequest 把 Postman request 对象映射为 HttpApi 列。
func parsePostmanRequest(req map[string]any) (method int16, uri string, params, headers, body model.JSON, err error) {
	m, ok := methodToEnum[strings.ToLower(fmt.Sprint(req["method"]))]
	if !ok {
		return 0, "", nil, nil, nil, fmt.Errorf("unsupported method %q", req["method"])
	}
	uri, query, err := parsePostmanURL(req["url"])
	if err != nil {
		return 0, "", nil, nil, nil, err
	}
	var hdrs [][2]string
	for _, hRaw := range asStepList(req["header"]) {
		key := strings.TrimSpace(fmt.Sprint(hRaw["key"]))
		val := fmt.Sprint(hRaw["value"])
		if key == "" || strings.EqualFold(key, "host") {
			continue // Postman 自动头忽略
		}
		hdrs = append(hdrs, [2]string{key, val})
	}
	raw, _ := req["body"].(map[string]any)
	if mode, _ := raw["mode"].(string); mode == "raw" {
		if text, _ := raw["raw"].(string); text != "" {
			ct := commonv1.BodyContentType_BODY_CONTENT_TYPE_X_WWW_FORM_URLENCODED
			if strings.HasPrefix(strings.TrimSpace(text), "{") || strings.HasPrefix(strings.TrimSpace(text), "[") {
				ct = commonv1.BodyContentType_BODY_CONTENT_TYPE_JSON
			}
			body = bodyJSON(ct, text)
		}
	}
	return m, uri, query, kvJSON(hdrs), body, nil
}

// parsePostmanURL 取 URL 的路径与 query（剥协议/host）。Postman 中 url 为对象：
// 优先 raw 字符串；无 raw 时由 path/host 数组重建（query 数组缺失时从 raw 拆）。
func parsePostmanURL(uRaw any) (path string, query model.JSON, err error) {
	// 裸字符串形态（非标准 Postman，兼容处理）
	if s, ok := uRaw.(string); ok && s != "" {
		return splitURL(s)
	}
	m, ok := uRaw.(map[string]any)
	if !ok {
		return "", nil, fmt.Errorf("request url missing")
	}
	if raw, ok := m["raw"].(string); ok && raw != "" {
		p, q, err := splitURL(raw)
		if err != nil {
			return "", nil, err
		}
		if len(q) > 0 || p != "" {
			return p, q, nil
		}
	}
	segs, _ := m["path"].([]any)
	var parts []string
	for _, seg := range segs {
		parts = append(parts, fmt.Sprint(seg))
	}
	if len(parts) == 0 {
		return "", nil, fmt.Errorf("request url path empty")
	}
	var qs [][2]string
	for _, qRaw := range asStepList(m["query"]) {
		qs = append(qs, [2]string{fmt.Sprint(qRaw["key"]), fmt.Sprint(qRaw["value"])})
	}
	return "/" + strings.Join(parts, "/"), kvJSON(qs), nil
}

// splitURL 拆出 URL 的路径与 query（`url.Parse` 的 RawQuery 拆为键值对）。
func splitURL(s string) (string, model.JSON, error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", nil, fmt.Errorf("invalid url %q: %v", s, err)
	}
	var qs [][2]string
	for k, vs := range u.Query() {
		for _, v := range vs {
			qs = append(qs, [2]string{k, v})
		}
	}
	return u.EscapedPath(), kvJSON(qs), nil
}

// ExportPostman 导出项目全部 HttpApi 为 Postman Collection v2.1（JSON）。
// url.raw 用 {{base_url}} 占位（环境变量注入），query/header/body 对应列直出。
func ExportPostman(db *gorm.DB, tenantID, projectID int64, title string) ([]byte, error) {
	var apis []model.HttpApi
	if err := db.Where("tenant_id = ? AND project_id = ?", tenantID, projectID).
		Order("uri, method").Find(&apis).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(apis))
	for _, a := range apis {
		methodName := strings.ToUpper(strings.TrimPrefix(
			commonv1.HttpMethod_name[int32(a.Method)], "HTTP_METHOD_"))
		if methodName == "" || methodName == "UNSPECIFIED" {
			continue
		}
		raw := "{{base_url}}" + a.URI
		var query []any
		for _, kv := range unmarshalKV(a.Params) {
			query = append(query, map[string]any{"key": kv[0], "value": kv[1]})
			if !strings.Contains(raw, "?") {
				raw += "?"
			} else {
				raw += "&"
			}
			raw += url.QueryEscape(kv[0]) + "=" + url.QueryEscape(kv[1])
		}
		var headers []any
		for _, kv := range unmarshalKV(a.Headers) {
			headers = append(headers, map[string]any{"key": kv[0], "value": kv[1]})
		}
		req := map[string]any{
			"method": methodName,
			"url":    map[string]any{"raw": raw, "query": query},
			"header": headers,
		}
		if bodyRaw := unmarshalBodyRaw(a.Body); bodyRaw != "" {
			lang := "json"
			if ct := bodyContentType(a.Body); ct == int64(commonv1.BodyContentType_BODY_CONTENT_TYPE_X_WWW_FORM_URLENCODED) {
				lang = "text"
			}
			req["body"] = map[string]any{
				"mode":    "raw",
				"raw":     bodyRaw,
				"options": map[string]any{"raw": map[string]any{"language": lang}},
			}
		}
		items = append(items, map[string]any{
			"name":    methodName + " " + a.URI,
			"request": req,
		})
	}
	// 保证输出稳定（Map 序列化已按 key 排序，此处对 items 再按 name 排）
	sort.Slice(items, func(i, j int) bool {
		return fmt.Sprint(items[i]["name"]) < fmt.Sprint(items[j]["name"])
	})
	col := map[string]any{
		"info": map[string]any{
			"name":   title,
			"schema": postmanSchema,
		},
		"item": items,
	}
	return json.MarshalIndent(col, "", "  ")
}
