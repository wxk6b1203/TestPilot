package httpserver

import (
	"reflect"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// ensureEntity 校验单个实体（case/suite/plan/environment/api/project）属于当前租户。
// 返回 false 时已写错误响应。
func ensureEntity(db *gorm.DB, ctx fiber.Ctx, kind string, id int64) bool {
	c := claimsOf(ctx)
	var tbl any
	switch kind {
	case "project":
		tbl = &model.Project{}
	case "environment":
		tbl = &model.Environment{}
	case "api":
		tbl = &model.HttpApi{}
	case "case":
		tbl = &model.TestCase{}
	case "suite":
		tbl = &model.TestSuite{}
	case "plan":
		tbl = &model.TestPlan{}
	default:
		writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "unknown ref kind "+kind))
		return false
	}
	var n int64
	if err := db.Model(tbl).Where("id = ? AND tenant_id = ?", id, c.TenantID).
		Count(&n).Error; err != nil || n == 0 {
		writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam,
			kind+" reference outside tenant"))
		return false
	}
	return true
}

// validateRefs 校验结构体中的引用 ID 字段（ProjectID/EnvID/APIID/CaseID/PlanID）
// 必须属于当前租户——防止跨租户数据关联污染（把资源挂到他租户的 project/env 下）。
// 返回 false 时已写错误响应。
func validateRefs(db *gorm.DB, ctx fiber.Ctx, v any) bool {
	c := claimsOf(ctx)
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return true
	}
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Type.Kind() != reflect.Int64 {
			continue
		}
		id := rv.Field(i).Int()
		if id == 0 {
			continue
		}
		var tbl any
		switch f.Name {
		case "ProjectID":
			tbl = &model.Project{}
		case "EnvID":
			tbl = &model.Environment{}
		case "APIID":
			tbl = &model.HttpApi{}
		case "CaseID":
			tbl = &model.TestCase{}
		case "PlanID":
			tbl = &model.TestPlan{}
		default:
			continue
		}
		var n int64
		if err := db.Model(tbl).Where("id = ? AND tenant_id = ?", id, c.TenantID).
			Count(&n).Error; err != nil || n == 0 {
			writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam,
				f.Name+" references entity outside tenant"))
			return false
		}
	}
	return true
}
