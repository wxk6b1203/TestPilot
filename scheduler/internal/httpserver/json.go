package httpserver

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/auth"
	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// writeJSON 序列化 v 为 JSON 响应；id / *_id / tenant_id 数字键统一转成字符串
// （雪花 ID 超出 JS Number 安全范围）。
func writeJSON(ctx fiber.Ctx, code int, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return ctx.Status(fiber.StatusInternalServerError).SendString(`{"error":"marshal failed"}`)
	}
	var doc any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return ctx.Status(fiber.StatusInternalServerError).SendString(`{"error":"encode failed"}`)
	}
	stringifyIDs(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		return ctx.Status(fiber.StatusInternalServerError).SendString(`{"error":"encode failed"}`)
	}
	ctx.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return ctx.Status(code).Send(out)
}

func stringifyIDs(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if n, ok := val.(json.Number); ok && isIDKey(k) {
				t[k] = n.String()
				continue
			}
			stringifyIDs(val)
		}
	case []any:
		for _, val := range t {
			stringifyIDs(val)
		}
	}
}

func isIDKey(k string) bool {
	return k == "id" || strings.HasSuffix(k, "_id")
}

// writeInternalErr 内部错误出口：细节进日志，客户端只见通用文案（防 SQLSTATE/路径泄漏）。
func writeInternalErr(ctx fiber.Ctx, err error) error {
	logging.L.Warnw("internal error", "path", ctx.Path(), "err", err)
	return writeErr(ctx, fiber.StatusInternalServerError, "internal error")
}

// writeErr 通用错误（按状态码映射到通用码）；领域错误请用 writeAppErr + apperr 构造器。
func writeErr(ctx fiber.Ctx, code int, msg string) error {
	c := apperr.CodeInternal
	switch code {
	case fiber.StatusBadRequest:
		c = apperr.CodeInvalidParam
	case fiber.StatusUnauthorized:
		c = apperr.CodeUnauthorized
	case fiber.StatusForbidden:
		c = apperr.CodeForbidden
	case fiber.StatusNotFound:
		c = apperr.CodeNotFound
	case fiber.StatusConflict:
		c = apperr.CodeConflict
	}
	return writeAppErr(ctx, apperr.New(code, c, msg))
}

// writeAppErr 输出结构化错误：{"error":{"code","message"}}。
func writeAppErr(ctx fiber.Ctx, err error) error {
	ae := apperr.From(err)
	return writeJSON(ctx, ae.HTTP, map[string]any{"error": ae})
}

func decode(ctx fiber.Ctx, v any) bool {
	raw := ctx.Body()
	if len(bytes.TrimSpace(raw)) == 0 {
		return true // 空 body 视为空对象
	}
	var doc any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		writeErr(ctx, fiber.StatusBadRequest, "invalid json: "+err.Error())
		return false
	}
	normalizeIDs(reflect.TypeOf(v), doc)
	buf, err := json.Marshal(doc)
	if err != nil {
		writeErr(ctx, fiber.StatusBadRequest, "invalid json")
		return false
	}
	if err := json.Unmarshal(buf, v); err != nil {
		writeErr(ctx, fiber.StatusBadRequest, "invalid json: "+err.Error())
		return false
	}
	return true
}

var timeType = reflect.TypeOf(time.Time{})

// normalizeIDs 按目标结构体类型，把 int64 字段上的字符串数字还原为数
// （API 输出把 ID 序列化为字符串，客户端回传时保持对称；definition 等
// RawMessage 列不受影响，内部 proto JSON 的字符串 ID 保持原样）。
func normalizeIDs(t reflect.Type, doc any) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			normalizeIDs(f.Type, doc) // 嵌入结构体共享同一 map
			continue
		}
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		val, ok := m[tag]
		if !ok {
			continue
		}
		ft := f.Type
		switch {
		case ft.Kind() == reflect.Int64:
			if s, ok := val.(string); ok {
				if _, err := strconv.ParseInt(s, 10, 64); err == nil {
					m[tag] = json.Number(s)
				}
			}
		case ft.Kind() == reflect.Struct && ft != timeType:
			normalizeIDs(ft, val)
		case ft.Kind() == reflect.Slice:
			et := ft.Elem()
			for et.Kind() == reflect.Ptr {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct && et != timeType && et.Name() != "RawMessage" {
				if arr, ok := val.([]any); ok {
					for _, item := range arr {
						normalizeIDs(et, item)
					}
				}
			}
		}
	}
}

func claimsOf(ctx fiber.Ctx) *auth.Claims {
	c, _ := auth.FromContext(ctx.Context())
	return c
}

// idList 解析 ["123", "456"] 或 [123, 456] 形式的 ID 数组（雪花 ID 超 JS 安全整数，
// 前端回传为字符串；内部使用 json.Number 保精度）。
type idList []int64

func (l *idList) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var arr []json.Number
	if err := dec.Decode(&arr); err != nil {
		return err
	}
	out := make([]int64, 0, len(arr))
	for _, n := range arr {
		id, err := strconv.ParseInt(string(n), 10, 64)
		if err != nil {
			return err
		}
		out = append(out, id)
	}
	*l = out
	return nil
}

func pathID(ctx fiber.Ctx, name string) (int64, bool) {
	id, err := strconv.ParseInt(ctx.Params(name), 10, 64)
	if err != nil {
		writeErr(ctx, fiber.StatusBadRequest, "invalid "+name)
		return 0, false
	}
	return id, true
}

func queryInt(ctx fiber.Ctx, name string) int64 {
	v, _ := strconv.ParseInt(ctx.Query(name), 10, 64)
	return v
}

func pageParams(ctx fiber.Ctx) (offset, limit int) {
	page := queryInt(ctx, "page")
	size := queryInt(ctx, "page_size")
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 500 {
		size = 200
	}
	return int((page - 1) * size), int(size)
}

// setIntField 通过反射给结构体的 int64 字段赋值（仅当当前为 0）。
func setIntField(v any, name string, val int64) {
	f := reflect.ValueOf(v).Elem().FieldByName(name)
	if f.IsValid() && f.CanSet() && f.Kind() == reflect.Int64 && f.Int() == 0 {
		f.SetInt(val)
	}
}

// forceIntField 强制覆盖 int64 字段。
func forceIntField(v any, name string, val int64) {
	f := reflect.ValueOf(v).Elem().FieldByName(name)
	if f.IsValid() && f.CanSet() && f.Kind() == reflect.Int64 {
		f.SetInt(val)
	}
}

// assignIDs 给新建实体强制分配主键与租户（无条件覆盖请求体中的 id/tenant_id——
// 防止客户端跨租户创建：setIntField 的条件赋值已被证明可被 body 注入绕过）。
func assignIDs(v any, tenantID int64) {
	forceIntField(v, "ID", model.NextID())
	forceIntField(v, "TenantID", tenantID)
}

// listOf 通用分页列表：apply 注入租户/过滤条件。
func listOf[T any](db *gorm.DB, ctx fiber.Ctx, apply func(*gorm.DB) *gorm.DB) error {
	var total int64
	if err := apply(db.Model(new(T))).Count(&total).Error; err != nil {
		return writeInternalErr(ctx, err)
	}
	items := make([]T, 0)
	offset, limit := pageParams(ctx)
	if err := apply(db).Order("id desc").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return writeInternalErr(ctx, err)
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"items": items, "total": total})
}

// createOf 通用创建：decode → 分配 ID/租户（强制覆盖）→ 落库。
func createOf[T any](db *gorm.DB, ctx fiber.Ctx, bind func(*T)) error {
	var v T
	if !decode(ctx, &v) {
		return nil
	}
	bind(&v)
	// 兜底强制：无论 bind 实现如何，ID/TenantID 一律以服务端为准
	forceIntField(&v, "ID", model.NextID())
	forceIntField(&v, "TenantID", claimsOf(ctx).TenantID)
	// C6：引用实体必须属于本租户
	if !validateRefs(db, ctx, &v) {
		return nil
	}
	if err := db.Create(&v).Error; err != nil {
		return writeInternalErr(ctx, err)
	}
	return writeJSON(ctx, fiber.StatusOK, &v)
}

// getOf 通用详情。
func getOf[T any](db *gorm.DB, ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var v T
	if err := db.Where("id = ? AND tenant_id = ?", id, c.TenantID).First(&v).Error; err != nil {
		return writeErr(ctx, fiber.StatusNotFound, "not found")
	}
	return writeJSON(ctx, fiber.StatusOK, &v)
}

// updateOf 通用更新：先取回实体，再把请求体解码覆盖到实体上（未传字段保持原值）。
func updateOf[T any](db *gorm.DB, ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var v T
	if err := db.Where("id = ? AND tenant_id = ?", id, c.TenantID).First(&v).Error; err != nil {
		return writeErr(ctx, fiber.StatusNotFound, "not found")
	}
	if !decode(ctx, &v) {
		return nil
	}
	// C6：请求体可能改写了引用 ID——先校验归属再落库
	if !validateRefs(db, ctx, &v) {
		return nil
	}
	forceIntField(&v, "ID", id) // ID/TenantID 不可变
	forceIntField(&v, "TenantID", c.TenantID)
	if err := db.Save(&v).Error; err != nil {
		return writeInternalErr(ctx, err)
	}
	return writeJSON(ctx, fiber.StatusOK, &v)
}

// deleteOf 通用删除（有 DeletedAt 的模型自动软删）。
func deleteOf[T any](db *gorm.DB, ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var v T
	res := db.Where("id = ? AND tenant_id = ?", id, c.TenantID).Delete(&v)
	if res.Error != nil {
		return writeErr(ctx, fiber.StatusInternalServerError, res.Error.Error())
	}
	if res.RowsAffected == 0 {
		return writeErr(ctx, fiber.StatusNotFound, "not found")
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
}
