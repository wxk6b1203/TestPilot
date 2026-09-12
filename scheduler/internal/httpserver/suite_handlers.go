package httpserver

import (
	"bytes"
	"encoding/json"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// suitePayload 套件 + 有序 case_ids（items 表按 order 展开）。
type suitePayload struct {
	model.TestSuite
	CaseIDs idList `json:"case_ids"`
}

func (s *Server) listSuites(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid := queryInt(ctx, "project_id")
	return listOf[model.TestSuite](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", c.TenantID)
		if pid != 0 {
			q = q.Where("project_id = ?", pid)
		}
		return q
	})
}

func (s *Server) createSuite(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in suitePayload
	if !decode(ctx, &in) {
		return nil
	}
	if !validateRefs(s.db, ctx, &in.TestSuite) {
		return nil
	}
	// 套件引用的 case 必须属于本租户（否则运行期被 runner 静默跳过/跨租户污染）
	for _, cid := range in.CaseIDs {
		if !ensureEntity(s.db, ctx, "case", cid) {
			return nil
		}
	}
	assignIDs(&in.TestSuite, c.TenantID)
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&in.TestSuite).Error; err != nil {
			return err
		}
		if err := replaceSuiteItems(tx, in.TestSuite.ID, in.CaseIDs); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return writeInternalErr(ctx, err)
	}
	return writeJSON(ctx, fiber.StatusOK, &in)
}

func (s *Server) getSuite(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var su model.TestSuite
	if err := s.db.Where("id = ? AND tenant_id = ?", id, c.TenantID).First(&su).Error; err != nil {
		return writeErr(ctx, fiber.StatusNotFound, "not found")
	}
	return writeJSON(ctx, fiber.StatusOK, &suitePayload{TestSuite: su, CaseIDs: suiteCaseIDs(s.db, id)})
}

func (s *Server) updateSuite(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var su model.TestSuite
	if err := s.db.Where("id = ? AND tenant_id = ?", id, c.TenantID).First(&su).Error; err != nil {
		return writeErr(ctx, fiber.StatusNotFound, "not found")
	}
	// case_ids 单独解码；套件本体合并到取回的实体上（未传字段保持原值）——
	// 解码到全新 struct 再 Save 全字段写，会把 created_at/project_id 等清零。
	// case_ids 未传 = 保持原成员（显式空数组才是清空），避免省略键误删全部条目
	var probe struct {
		CaseIDs json.RawMessage `json:"case_ids"`
	}
	if !decode(ctx, &probe) {
		return nil
	}
	hasCases := len(probe.CaseIDs) > 0 && !bytes.Equal(probe.CaseIDs, []byte("null"))
	var in struct {
		CaseIDs idList `json:"case_ids"`
	}
	if hasCases {
		if !decode(ctx, &in) {
			return nil
		}
	}
	if !decode(ctx, &su) {
		return nil
	}
	forceIntField(&su, "ID", id) // ID/TenantID 不可变（body 注入不得改写主键/归属）
	forceIntField(&su, "TenantID", c.TenantID)
	// C6：body 可能改写 project_id——校验归属后再落库
	if !validateRefs(s.db, ctx, &su) {
		return nil
	}
	// 套件引用的 case 必须属于本租户（对齐 createSuite）
	for _, cid := range in.CaseIDs {
		if !ensureEntity(s.db, ctx, "case", cid) {
			return nil
		}
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&su).Error; err != nil {
			return err
		}
		if !hasCases {
			return nil
		}
		return replaceSuiteItems(tx, su.ID, in.CaseIDs)
	})
	if err != nil {
		return writeInternalErr(ctx, err)
	}
	return writeJSON(ctx, fiber.StatusOK, &suitePayload{TestSuite: su, CaseIDs: suiteCaseIDs(s.db, su.ID)})
}

func (s *Server) deleteSuite(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND tenant_id = ?", id, c.TenantID).Delete(&model.TestSuite{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Where("suite_id = ?", id).Delete(&model.TestSuiteItem{}).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id = ? AND node_type = ? AND ref_id = ?",
			c.TenantID, model.NodeTypeSuite, id).Delete(&model.TreeNode{}).Error
	})
	if err == gorm.ErrRecordNotFound {
		return writeErr(ctx, fiber.StatusNotFound, "not found")
	}
	if err != nil {
		return writeInternalErr(ctx, err)
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
}

// replaceSuiteItems 全量替换套件成员（order = 数组下标）。
func replaceSuiteItems(tx *gorm.DB, suiteID int64, caseIDs idList) error {
	if err := tx.Where("suite_id = ?", suiteID).Delete(&model.TestSuiteItem{}).Error; err != nil {
		return err
	}
	for i, cid := range caseIDs {
		item := model.TestSuiteItem{ID: model.NextID(), SuiteID: suiteID, CaseID: cid, Order: i}
		if err := tx.Create(&item).Error; err != nil {
			return err
		}
	}
	return nil
}

// suiteCaseIDs 读取套件的有序 case id 列表。
func suiteCaseIDs(db *gorm.DB, suiteID int64) idList {
	var items []model.TestSuiteItem
	db.Where("suite_id = ?", suiteID).Order("\"order\" asc").Find(&items)
	out := make(idList, 0, len(items))
	for _, it := range items {
		out = append(out, it.CaseID)
	}
	return out
}
