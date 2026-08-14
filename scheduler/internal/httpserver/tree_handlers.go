package httpserver

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v3"
	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// ---- 目录树（tree_nodes 最小落地：folder CRUD + 接口挂载/移动 + 树查询）----
// 节点模型遵循 DDL：folder 为纯目录节点（ref_id=0）；接口挂载为 node_type=http_api 的引用节点。

type treeNodeView struct {
	ID       int64           `json:"id"`
	NodeType int16           `json:"node_type"`
	Name     string          `json:"name"`
	RefID    int64           `json:"ref_id,omitempty"`
	Ref      map[string]any  `json:"ref,omitempty"` // http_api 摘要（动态取，非快照）
	Children []*treeNodeView `json:"children,omitempty"`
}

// getProjectTree 返回项目目录树（folder 嵌套；api 节点带 method/uri/name 摘要）。
func (s *Server) getProjectTree(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid := queryInt(ctx, "project_id")
	if pid == 0 {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "project_id required"))
	}
	var nodes []model.TreeNode
	if err := s.db.Where("tenant_id = ? AND project_id = ?", c.TenantID, pid).
		Order("\"order\" asc, id asc").Find(&nodes).Error; err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	// api 摘要批量加载
	apiIDs := make([]int64, 0)
	for _, n := range nodes {
		if n.NodeType == model.NodeTypeHTTPAPI && n.RefID != 0 {
			apiIDs = append(apiIDs, n.RefID)
		}
	}
	apiByName := map[int64]model.HttpApi{}
	if len(apiIDs) > 0 {
		var apis []model.HttpApi
		s.db.Where("tenant_id = ? AND id IN ?", c.TenantID, apiIDs).Find(&apis)
		for _, a := range apis {
			apiByName[a.ID] = a
		}
	}
	byParent := map[int64][]*treeNodeView{}
	for _, n := range nodes {
		v := &treeNodeView{ID: n.ID, NodeType: n.NodeType, Name: n.Name, RefID: n.RefID}
		if a, ok := apiByName[n.RefID]; ok && n.NodeType == model.NodeTypeHTTPAPI {
			name := a.Name
			if name == "" {
				name = strings.ToUpper(strings.TrimPrefix(
					httpMethodName(a.Method), "HTTP_METHOD_"))
				name = name + " " + a.URI
			}
			v.Ref = map[string]any{
				"id": a.ID, "method": a.Method, "uri": a.URI, "name": name,
			}
		}
		byParent[n.ParentID] = append(byParent[n.ParentID], v)
	}
	// 组装：根节点 parent_id=0
	for _, v := range byParent[0] {
		attachChildren(v, byParent)
	}
	roots := byParent[0]
	if roots == nil {
		roots = []*treeNodeView{}
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"tree": roots})
}

func attachChildren(n *treeNodeView, byParent map[int64][]*treeNodeView) {
	for _, c := range byParent[n.ID] {
		attachChildren(c, byParent)
		n.Children = append(n.Children, c)
	}
}

// httpMethodName 方法枚举名（展示兜底）。
func httpMethodName(m int16) string {
	return commonv1.HttpMethod_name[int32(m)]
}

type folderReq struct {
	ProjectID int64  `json:"project_id"`
	Name      string `json:"name"`
	ParentID  int64  `json:"parent_id"`
}

// createFolder 新建目录节点。
func (s *Server) createFolder(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in folderReq
	if !decode(ctx, &in) {
		return nil
	}
	if in.ProjectID == 0 || strings.TrimSpace(in.Name) == "" {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "project_id 与 name 必填"))
	}
	path, err := s.nodePath(c.TenantID, in.ParentID)
	if err != nil {
		return writeAppErr(ctx, apperr.From(err))
	}
	n := &model.TreeNode{
		ID: model.NextID(), TenantID: c.TenantID, ProjectID: in.ProjectID,
		ParentID: in.ParentID, NodeType: model.NodeTypeFolder,
		Name: strings.TrimSpace(in.Name), Path: path + fmt.Sprint(model.NextID()) + "/",
	}
	n.Path = path + fmt.Sprint(n.ID) + "/"
	if err := s.db.Create(n).Error; err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, n)
}

// nodePath 父节点路径（根 = ""）。
func (s *Server) nodePath(tenantID, parentID int64) (string, error) {
	if parentID == 0 {
		return "", nil
	}
	var p model.TreeNode
	if err := s.db.Where("id = ? AND tenant_id = ?", parentID, tenantID).First(&p).Error; err != nil {
		return "", apperr.NotFound(apperr.CodeNotFound, "parent folder not found")
	}
	if p.NodeType != model.NodeTypeFolder {
		return "", apperr.BadRequest(apperr.CodeInvalidParam, "parent is not a folder")
	}
	return p.Path, nil
}

// renameFolder 重命名目录。
func (s *Server) renameFolder(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var in struct {
		Name string `json:"name"`
	}
	if !decode(ctx, &in) {
		return nil
	}
	if strings.TrimSpace(in.Name) == "" {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "name 必填"))
	}
	res := s.db.Model(&model.TreeNode{}).
		Where("id = ? AND tenant_id = ? AND node_type = ?", id, c.TenantID, model.NodeTypeFolder).
		Update("name", strings.TrimSpace(in.Name))
	if res.Error != nil {
		return writeAppErr(ctx, apperr.Internal(res.Error.Error()))
	}
	if res.RowsAffected == 0 {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "folder not found"))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
}

// deleteFolder 删除目录：级联删子孙节点（接口仅摘挂，不删接口实体）。
func (s *Server) deleteFolder(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var f model.TreeNode
	if err := s.db.Where("id = ? AND tenant_id = ? AND node_type = ?", id, c.TenantID,
		model.NodeTypeFolder).First(&f).Error; err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "folder not found"))
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND path LIKE ?", c.TenantID, f.Path+"%").
			Delete(&model.TreeNode{}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
}

type mountReq struct {
	ProjectID int64 `json:"project_id"`
	APIID     int64 `json:"api_id"`
	ParentID  int64 `json:"parent_id"`
}

// mountAPI 把接口挂到目录（已有挂载 → 409；未挂载时自动建节点）。
func (s *Server) mountAPI(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in mountReq
	if !decode(ctx, &in) {
		return nil
	}
	if in.ProjectID == 0 || in.APIID == 0 {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "project_id 与 api_id 必填"))
	}
	var api model.HttpApi
	if err := s.db.Where("id = ? AND tenant_id = ?", in.APIID, c.TenantID).First(&api).Error; err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "api not found"))
	}
	path, err := s.nodePath(c.TenantID, in.ParentID)
	if err != nil {
		return writeAppErr(ctx, apperr.From(err))
	}
	var exist int64
	s.db.Model(&model.TreeNode{}).
		Where("tenant_id = ? AND node_type = ? AND ref_id = ?", c.TenantID, model.NodeTypeHTTPAPI, in.APIID).
		Count(&exist)
	if exist > 0 {
		return writeAppErr(ctx, apperr.Conflict(apperr.CodeConflict, "api already mounted"))
	}
	name := api.Name
	if name == "" {
		name = httpMethodName(api.Method) + " " + api.URI
	}
	n := &model.TreeNode{
		ID: model.NextID(), TenantID: c.TenantID, ProjectID: in.ProjectID,
		ParentID: in.ParentID, NodeType: model.NodeTypeHTTPAPI, RefID: api.ID,
		Name: name, Path: path + fmt.Sprint(model.NextID()) + "/",
	}
	n.Path = path + fmt.Sprint(n.ID) + "/"
	if err := s.db.Create(n).Error; err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, n)
}

// moveNode 移动节点到新父目录（自身与子孙 path 前缀重算）。
func (s *Server) moveNode(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var in struct {
		ParentID int64 `json:"parent_id"`
	}
	if !decode(ctx, &in) {
		return nil
	}
	newPath, err := s.nodePath(c.TenantID, in.ParentID)
	if err != nil {
		return writeAppErr(ctx, apperr.From(err))
	}
	var n model.TreeNode
	if err := s.db.Where("id = ? AND tenant_id = ?", id, c.TenantID).First(&n).Error; err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "node not found"))
	}
	if in.ParentID == n.ParentID {
		return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
	}
	oldPrefix := n.Path
	err = s.db.Transaction(func(tx *gorm.DB) error {
		// 自身 + 子孙（path 前缀匹配）
		var subs []model.TreeNode
		if err := tx.Where("tenant_id = ? AND path LIKE ?", c.TenantID, oldPrefix+"%").
			Find(&subs).Error; err != nil {
			return err
		}
		for _, sub := range subs {
			suffix := strings.TrimPrefix(sub.Path, oldPrefix)
			parent := sub.ParentID
			if sub.ID == n.ID {
				parent = in.ParentID
			}
			if err := tx.Model(&sub).Updates(map[string]any{
				"parent_id": parent,
				"path":      newPath + fmt.Sprint(n.ID) + "/" + suffix,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
}

// unmountAPI 摘挂接口（只删树节点，不删接口）。
func (s *Server) unmountAPI(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	res := s.db.Where("id = ? AND tenant_id = ? AND node_type = ?", id, c.TenantID,
		model.NodeTypeHTTPAPI).Delete(&model.TreeNode{})
	if res.Error != nil {
		return writeAppErr(ctx, apperr.Internal(res.Error.Error()))
	}
	if res.RowsAffected == 0 {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "mount node not found"))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
}
