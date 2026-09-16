package httpserver

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// ---- 数据模型（结构）：JSON Schema 形态的结构定义 CRUD + 目录树挂载 ----

func (s *Server) listDataModels(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid := queryInt(ctx, "project_id")
	return listOf[model.DataModel](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", c.TenantID)
		if pid != 0 {
			q = q.Where("project_id = ?", pid)
		}
		return q
	})
}

type dataModelReq struct {
	model.DataModel
	ParentNodeID int64 `json:"parent_node_id"` // 可选：目标目录树节点（folder）id，0=挂根
}

// createDataModel 创建数据模型；指定 parent_node_id 时同事务内挂到该目录（与 createAPI 同构）。
func (s *Server) createDataModel(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in dataModelReq
	if !decode(ctx, &in) {
		return nil
	}
	v := in.DataModel
	if strings.TrimSpace(v.Name) == "" {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "name is required"))
	}
	assignIDs(&v, c.TenantID)
	// C6：project_id 必须属于本租户（自定义创建路径不走 createOf，需单独校验）
	if !validateRefs(s.db, ctx, &v) {
		return nil
	}
	// 指定目录时校验并取父路径；未指定挂根（根即普通目录）
	parentPath := ""
	if in.ParentNodeID != 0 {
		p, err := s.nodePath(c.TenantID, in.ParentNodeID)
		if err != nil {
			return writeAppErr(ctx, apperr.From(err))
		}
		parentPath = p
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&v).Error; err != nil {
			return err
		}
		cnt, err := childCount(tx, c.TenantID, in.ParentNodeID) // 追加到目标目录末尾
		if err != nil {
			return err
		}
		n := &model.TreeNode{
			ID: model.NextID(), TenantID: c.TenantID, ProjectID: v.ProjectID,
			ParentID: in.ParentNodeID, NodeType: model.NodeTypeDataMdl, RefID: v.ID,
			Name: v.Name, Order: cnt,
		}
		n.Path = parentPath + fmt.Sprint(n.ID) + "/"
		return tx.Create(n).Error
	})
	if err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, &v)
}

func (s *Server) getDataModel(ctx fiber.Ctx) error { return getOf[model.DataModel](s.db, ctx) }
func (s *Server) updateDataModel(ctx fiber.Ctx) error {
	return updateOf[model.DataModel](s.db, ctx)
}

// deleteDataModel 删除数据模型并级联删除其目录树挂载节点（与 deleteAPI 同构）。
func (s *Server) deleteDataModel(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var v model.DataModel
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND tenant_id = ?", id, c.TenantID).Delete(&v)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return apperr.NotFound(apperr.CodeNotFound, "data model not found")
		}
		return tx.Where("tenant_id = ? AND node_type = ? AND ref_id = ?",
			c.TenantID, model.NodeTypeDataMdl, id).Delete(&model.TreeNode{}).Error
	})
	if err != nil {
		return writeAppErr(ctx, apperr.From(err))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
}
