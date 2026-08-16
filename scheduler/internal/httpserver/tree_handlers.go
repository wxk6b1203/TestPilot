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
// 顺序约定：同父节点按 order 升序；新建/挂载缺省追加末尾（order=子节点数）；
// 移动/挂载可选 index 精确插入（>= index 的兄弟后移一位）。

// childCount 父节点现有子节点数（用于追加到末尾的 order）。
func childCount(db *gorm.DB, tenantID, parentID int64) (int, error) {
	var cnt int64
	if err := db.Model(&model.TreeNode{}).
		Where("tenant_id = ? AND parent_id = ?", tenantID, parentID).Count(&cnt).Error; err != nil {
		return 0, err
	}
	return int(cnt), nil
}

// clampIndex 归一化插入位置：nil=末尾；负数→0；越界→末尾。
func clampIndex(idx *int, cnt int) int {
	if idx == nil {
		return cnt
	}
	v := *idx
	if v < 0 {
		v = 0
	}
	if v > cnt {
		v = cnt
	}
	return v
}

// shiftOrders 把目标父节点下 order >= from 的兄弟后移一位（为 index 插入腾位）。
func shiftOrders(tx *gorm.DB, tenantID, parentID int64, from int, excludeID int64) error {
	return tx.Model(&model.TreeNode{}).
		Where("tenant_id = ? AND parent_id = ? AND id <> ? AND \"order\" >= ?", tenantID, parentID, excludeID, from).
		UpdateColumn("order", gorm.Expr(`"order" + 1`)).Error
}

type treeNodeView struct {
	ID       int64           `json:"id"`
	NodeType int16           `json:"node_type"`
	Name     string          `json:"name"`
	RefID    int64           `json:"ref_id,omitempty"`
	Ref      map[string]any  `json:"ref,omitempty"` // http_api 摘要（动态取，非快照）
	Children []*treeNodeView `json:"children,omitempty"`
}

// getProjectTree 返回项目目录树。kind=api（默认）/ case / suite 时只返回对应叶子与有效目录分支。
func (s *Server) getProjectTree(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid := queryInt(ctx, "project_id")
	if pid == 0 {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "project_id required"))
	}
	kind := strings.ToLower(ctx.Query("kind", "api"))
	var leafTypes []int16
	switch kind {
	case "case", "cases":
		leafTypes = []int16{model.NodeTypeTestCase}
	case "suite", "suites":
		leafTypes = []int16{model.NodeTypeSuite}
	default:
		leafTypes = []int16{model.NodeTypeHTTPAPI}
	}
	leafSet := map[int16]bool{}
	for _, t := range leafTypes {
		leafSet[t] = true
	}

	var nodes []model.TreeNode
	if err := s.db.Where("tenant_id = ? AND project_id = ?", c.TenantID, pid).
		Order("\"order\" asc, id asc").Find(&nodes).Error; err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}

	// 按 kind 批量加载叶子摘要
	caseByName := map[int64]model.TestCase{}
	suiteByName := map[int64]model.TestSuite{}
	apiByName := map[int64]model.HttpApi{}
	for _, n := range nodes {
		if n.RefID == 0 {
			continue
		}
		switch n.NodeType {
		case model.NodeTypeHTTPAPI:
			apiIDs := []int64{n.RefID}
			var apis []model.HttpApi
			if err := s.db.Where("tenant_id = ? AND id IN ?", c.TenantID, apiIDs).Find(&apis).Error; err != nil {
				return writeAppErr(ctx, apperr.Internal(err.Error()))
			}
			for _, a := range apis {
				apiByName[a.ID] = a
			}
		case model.NodeTypeTestCase:
			var cs []model.TestCase
			if err := s.db.Where("tenant_id = ? AND id = ?", c.TenantID, n.RefID).Find(&cs).Error; err != nil {
				return writeAppErr(ctx, apperr.Internal(err.Error()))
			}
			for _, x := range cs {
				caseByName[x.ID] = x
			}
		case model.NodeTypeSuite:
			var ss []model.TestSuite
			if err := s.db.Where("tenant_id = ? AND id = ?", c.TenantID, n.RefID).Find(&ss).Error; err != nil {
				return writeAppErr(ctx, apperr.Internal(err.Error()))
			}
			for _, x := range ss {
				suiteByName[x.ID] = x
			}
		}
	}

	byParent := map[int64][]*treeNodeView{}
	for _, n := range nodes {
		if n.NodeType != model.NodeTypeFolder && !leafSet[n.NodeType] {
			continue
		}
		v := &treeNodeView{ID: n.ID, NodeType: n.NodeType, Name: n.Name, RefID: n.RefID}
		switch n.NodeType {
		case model.NodeTypeHTTPAPI:
			if a, ok := apiByName[n.RefID]; ok {
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
		case model.NodeTypeTestCase:
			if c, ok := caseByName[n.RefID]; ok {
				v.Ref = map[string]any{"id": c.ID, "type": c.Type, "name": c.Name, "description": c.Description}
			}
		case model.NodeTypeSuite:
			if s, ok := suiteByName[n.RefID]; ok {
				v.Ref = map[string]any{"id": s.ID, "name": s.Name, "description": s.Description}
			}
		}
		byParent[n.ParentID] = append(byParent[n.ParentID], v)
	}
	// 组装：根节点 parent_id=0
	for _, v := range byParent[0] {
		attachChildren(v, byParent, 0)
	}
	roots := make([]*treeNodeView, 0, len(byParent[0]))
	for _, r := range byParent[0] {
		if filterTreeNode(r, leafSet) {
			roots = append(roots, r)
		}
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"tree": roots})
}

// filterTreeNode 剪掉不包含当前 kind 叶子节点的空目录分支；返回该节点是否应保留。
func filterTreeNode(n *treeNodeView, leafSet map[int16]bool) bool {
	if n.NodeType != model.NodeTypeFolder {
		return leafSet[n.NodeType]
	}
	kept := n.Children[:0]
	for _, c := range n.Children {
		if filterTreeNode(c, leafSet) {
			kept = append(kept, c)
		}
	}
	n.Children = kept
	return len(kept) > 0
}

// attachChildren 递归组装子树；depth 上限防止历史坏数据（环）导致栈溢出。
const maxTreeDepth = 64

func attachChildren(n *treeNodeView, byParent map[int64][]*treeNodeView, depth int) {
	if depth > maxTreeDepth {
		return
	}
	for _, c := range byParent[n.ID] {
		attachChildren(c, byParent, depth+1)
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
	order, err := childCount(s.db, c.TenantID, in.ParentID) // 新建目录追加到末尾
	if err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	n := &model.TreeNode{
		ID: model.NextID(), TenantID: c.TenantID, ProjectID: in.ProjectID,
		ParentID: in.ParentID, NodeType: model.NodeTypeFolder,
		Name: strings.TrimSpace(in.Name), Path: path + fmt.Sprint(model.NextID()) + "/",
		Order: order,
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
	// 兼容旧接口：仅传 api_id 时按 HTTP 接口处理
	APIID    int64 `json:"api_id"`
	RefType  int16 `json:"ref_type"` // 4=test_case 5=test_suite；缺省 0 时按 api_id 推导
	RefID    int64 `json:"ref_id"`
	ParentID int64 `json:"parent_id"`
	Index    *int  `json:"index"` // 可选：插入位置；缺省追加末尾
}

// mountNode 把接口/用例/套件挂到目录（已有挂载 → 409；未挂载时自动建节点）。
func (s *Server) mountAPI(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in mountReq
	if !decode(ctx, &in) {
		return nil
	}
	if in.ProjectID == 0 {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "project_id 必填"))
	}
	refType := in.RefType
	refID := in.RefID
	if refID == 0 {
		refID = in.APIID
	}
	if refType == 0 {
		refType = model.NodeTypeHTTPAPI
	}
	if refID == 0 || (refType != model.NodeTypeHTTPAPI && refType != model.NodeTypeTestCase && refType != model.NodeTypeSuite) {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "ref_type/ref_id 不合法"))
	}

	name := ""
	switch refType {
	case model.NodeTypeHTTPAPI:
		var api model.HttpApi
		if err := s.db.Where("id = ? AND tenant_id = ?", refID, c.TenantID).First(&api).Error; err != nil {
			return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "api not found"))
		}
		name = api.Name
		if name == "" {
			name = httpMethodName(api.Method) + " " + api.URI
		}
	case model.NodeTypeTestCase:
		var tc model.TestCase
		if err := s.db.Where("id = ? AND tenant_id = ?", refID, c.TenantID).First(&tc).Error; err != nil {
			return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "case not found"))
		}
		name = tc.Name
	case model.NodeTypeSuite:
		var su model.TestSuite
		if err := s.db.Where("id = ? AND tenant_id = ?", refID, c.TenantID).First(&su).Error; err != nil {
			return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "suite not found"))
		}
		name = su.Name
	}

	path, err := s.nodePath(c.TenantID, in.ParentID)
	if err != nil {
		return writeAppErr(ctx, apperr.From(err))
	}
	var exist int64
	s.db.Model(&model.TreeNode{}).
		Where("tenant_id = ? AND node_type = ? AND ref_id = ?", c.TenantID, refType, refID).
		Count(&exist)
	if exist > 0 {
		return writeAppErr(ctx, apperr.Conflict(apperr.CodeConflict, "entity already mounted"))
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		cnt, err := childCount(tx, c.TenantID, in.ParentID)
		if err != nil {
			return err
		}
		order := clampIndex(in.Index, cnt)
		if in.Index != nil && order < cnt {
			if err := shiftOrders(tx, c.TenantID, in.ParentID, order, 0); err != nil {
				return err
			}
		}
		n := &model.TreeNode{
			ID: model.NextID(), TenantID: c.TenantID, ProjectID: in.ProjectID,
			ParentID: in.ParentID, NodeType: refType, RefID: refID,
			Name: name, Order: order,
		}
		n.Path = path + fmt.Sprint(n.ID) + "/"
		return tx.Create(n).Error
	})
	if err != nil {
		return writeAppErr(ctx, apperr.Internal(err.Error()))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
}

// moveNode 移动节点到新父目录（自身与子孙 path 前缀重算；可选 index 精确插入）。
func (s *Server) moveNode(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var in struct {
		ParentID int64 `json:"parent_id"`
		Index    *int  `json:"index"` // 可选：目标父目录中的插入位置；缺省追加末尾
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
	// 环检测：目标父节点不能是自身或其子孙（自身 path 是子孙 path 前缀）
	if newPath != "" && (newPath == n.Path || strings.HasPrefix(newPath, n.Path)) {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam,
			"cannot move node into its own subtree"))
	}
	oldPrefix := n.Path
	err = s.db.Transaction(func(tx *gorm.DB) error {
		// 自身 + 子孙（path 前缀匹配）
		var subs []model.TreeNode
		if err := tx.Where("tenant_id = ? AND path LIKE ?", c.TenantID, oldPrefix+"%").
			Find(&subs).Error; err != nil {
			return err
		}
		// 目标位置：缺省追加末尾；指定 index 时把 >= index 的兄弟后移一位
		cnt, err := childCount(tx, c.TenantID, in.ParentID)
		if err != nil {
			return err
		}
		targetOrder := clampIndex(in.Index, cnt)
		if in.Index != nil && targetOrder < cnt {
			if err := shiftOrders(tx, c.TenantID, in.ParentID, targetOrder, n.ID); err != nil {
				return err
			}
		}
		for _, sub := range subs {
			suffix := strings.TrimPrefix(sub.Path, oldPrefix)
			parent := sub.ParentID
			order := sub.Order
			if sub.ID == n.ID {
				parent = in.ParentID
				order = targetOrder
			}
			if err := tx.Model(&sub).Updates(map[string]any{
				"parent_id": parent,
				"path":      newPath + fmt.Sprint(n.ID) + "/" + suffix,
				"order":     order,
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

// unmountAPI 摘挂接口/用例/套件（只删树节点，不删实体）。
func (s *Server) unmountAPI(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	res := s.db.Where("id = ? AND tenant_id = ? AND node_type IN ?", id, c.TenantID,
		[]int16{model.NodeTypeHTTPAPI, model.NodeTypeTestCase, model.NodeTypeSuite}).Delete(&model.TreeNode{})
	if res.Error != nil {
		return writeAppErr(ctx, apperr.Internal(res.Error.Error()))
	}
	if res.RowsAffected == 0 {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "mount node not found"))
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"ok": true})
}

// reorderTree 重排同一父节点下的子节点顺序（ids = 完整有序子节点 id 列表）。
func (s *Server) reorderTree(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in struct {
		ParentID int64  `json:"parent_id"`
		IDs      idList `json:"ids"`
	}
	if !decode(ctx, &in) {
		return nil
	}
	if len(in.IDs) == 0 {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "ids 必填"))
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		for i, id := range in.IDs {
			if err := tx.Model(&model.TreeNode{}).
				Where("id = ? AND tenant_id = ?", id, c.TenantID).
				Update("order", i).Error; err != nil {
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
