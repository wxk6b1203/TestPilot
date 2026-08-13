package httpserver

import (
	"fmt"
	"github.com/gofiber/fiber/v3"

	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// ---- 压测计划 / 运行 ----

func (s *Server) listStressPlans(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	pid := queryInt(ctx, "project_id")
	return listOf[model.StressTestPlan](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", c.TenantID)
		if pid != 0 {
			q = q.Where("project_id = ?", pid)
		}
		return q
	})
}

func (s *Server) createStressPlan(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	return createOf[model.StressTestPlan](s.db, ctx, func(v *model.StressTestPlan) {
		v.ID = model.NextID()
		v.TenantID = c.TenantID
		if v.MetricsIntervalMs <= 0 {
			v.MetricsIntervalMs = 1000
		}
		if v.WorkerCount <= 0 {
			v.WorkerCount = 1
		}
	})
}

func (s *Server) getStressPlan(ctx fiber.Ctx) error {
	return getOf[model.StressTestPlan](s.db, ctx)
}

func (s *Server) updateStressPlan(ctx fiber.Ctx) error {
	return updateOf[model.StressTestPlan](s.db, ctx)
}

func (s *Server) deleteStressPlan(ctx fiber.Ctx) error {
	return deleteOf[model.StressTestPlan](s.db, ctx)
}

type stressRunReq struct {
	EnvID int64 `json:"env_id"`
}

func (s *Server) runStressPlan(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	planID, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var in stressRunReq
	if len(ctx.Body()) > 0 {
		if !decode(ctx, &in) {
			return nil
		}
	}
	runID, err := s.run.TriggerStress(ctx.Context(), c.TenantID, planID, in.EnvID, fmt.Sprint(c.UserID))
	if err != nil {
		return writeAppErr(ctx, err)
	}
	return writeJSON(ctx, fiber.StatusOK, map[string]any{"run_id": runID})
}

func (s *Server) listStressRuns(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	planID := queryInt(ctx, "plan_id")
	return listOf[model.StressRun](s.db, ctx, func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", c.TenantID)
		if planID != 0 {
			q = q.Where("stress_plan_id = ?", planID)
		}
		return q.Order("id desc")
	})
}

type stressRunView struct {
	model.StressRun
	Metrics []model.StressMetricPoint `json:"metrics"`
}

func (s *Server) getStressRun(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	id, ok := pathID(ctx, "id")
	if !ok {
		return nil
	}
	var run model.StressRun
	if err := s.db.Where("id = ? AND tenant_id = ?", id, c.TenantID).First(&run).Error; err != nil {
		return writeAppErr(ctx, apperr.NotFound(apperr.CodeNotFound, "stress run not found"))
	}
	points := make([]model.StressMetricPoint, 0)
	s.db.Where("stress_run_id = ? AND tenant_id = ?", id, c.TenantID).Order("ts asc").Find(&points)
	return writeJSON(ctx, fiber.StatusOK, &stressRunView{StressRun: run, Metrics: points})
}
