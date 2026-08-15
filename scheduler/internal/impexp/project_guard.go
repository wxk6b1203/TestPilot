package impexp

import (
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// ensureProject 校验 project 属于该租户（防跨租户引用注入：把 API 挂到他租户的 project 下）。
// 所有导入入口（REST + gRPC 共用 impexp 包）必须先过此关。
func ensureProject(db *gorm.DB, tenantID, projectID int64) error {
	var n int64
	if err := db.Model(&model.Project{}).Where("id = ? AND tenant_id = ?", projectID, tenantID).
		Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return apperr.BadRequest(apperr.CodeInvalidParam, "project not found in tenant")
	}
	return nil
}
