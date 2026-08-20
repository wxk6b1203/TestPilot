package httpserver

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// ---- 实时进度推送（SSE） ----
//
// GET /api/v1/events?channels=run:1,project:2,stress:3,workers
// 设计见 docs/ci-migration-plan.md（或后续 dedicated doc）：只推送“变化事件”，
// 完整快照仍走 REST；断线由客户端重连。

const maxEventChannels = 8

// sseHeartbeatInterval SSE 心跳间隔；测试中可缩短以加速收尾。
var sseHeartbeatInterval = 15 * time.Second

func (s *Server) eventsStream(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	if s.disp == nil {
		return writeErr(ctx, fiber.StatusServiceUnavailable, "events disabled")
	}
	channels, ok := s.parseEventChannels(ctx, c.TenantID)
	if !ok {
		return nil // 错误响应已写出
	}
	sub := s.disp.Events().Subscribe(channels)

	ctx.Set(fiber.HeaderContentType, "text/event-stream; charset=utf-8")
	ctx.Set(fiber.HeaderCacheControl, "no-cache")
	ctx.Set(fiber.HeaderConnection, "keep-alive")
	ctx.Set("X-Accel-Buffering", "no")
	ctx.Response().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer sub.Close()
		heartbeat := time.NewTicker(sseHeartbeatInterval)
		defer heartbeat.Stop()
		for {
			select {
			case e := <-sub.C:
				data, err := json.Marshal(e.Data)
				if err != nil {
					data = []byte(`{"error":"marshal event"}`)
				}
				if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, data); err != nil {
					return
				}
				if err := w.Flush(); err != nil {
					return
				}
			case <-heartbeat.C:
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					return
				}
				if err := w.Flush(); err != nil {
					return
				}
			case <-ctx.Context().Done():
				return
			}
		}
	})
	return nil
}

// parseEventChannels 解析并校验 channels query；每个 channel 必须属于当前租户。
func (s *Server) parseEventChannels(ctx fiber.Ctx, tenantID int64) ([]string, bool) {
	raw := strings.TrimSpace(ctx.Query("channels", ""))
	if raw == "" {
		_ = writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "channels required"))
		return nil, false
	}
	parts := strings.Split(raw, ",")
	if len(parts) == 0 || len(parts) > maxEventChannels {
		_ = writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "channels must contain 1-8 entries"))
		return nil, false
	}
	seen := map[string]bool{}
	channels := make([]string, 0, len(parts))
	for _, p := range parts {
		ch := strings.TrimSpace(p)
		if ch == "" || seen[ch] {
			continue
		}
		if ch == "workers" {
			seen[ch] = true
			channels = append(channels, ch)
			continue
		}
		prefix, value, hasColon := strings.Cut(ch, ":")
		if !hasColon || value == "" {
			_ = writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "invalid channel: "+ch))
			return nil, false
		}
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			_ = writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "invalid channel id: "+ch))
			return nil, false
		}
		if !s.channelBelongsToTenant(prefix, id, tenantID) {
			_ = writeErr(ctx, fiber.StatusNotFound, "channel not found: "+ch)
			return nil, false
		}
		seen[ch] = true
		channels = append(channels, ch)
	}
	if len(channels) == 0 {
		_ = writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "channels required"))
		return nil, false
	}
	return channels, true
}

func (s *Server) channelBelongsToTenant(prefix string, id, tenantID int64) bool {
	switch prefix {
	case "run":
		return recordExists(s.db, &model.TestRun{}, id, tenantID)
	case "stress":
		return recordExists(s.db, &model.StressRun{}, id, tenantID)
	case "project":
		return recordExists(s.db, &model.Project{}, id, tenantID)
	default:
		return false
	}
}

func recordExists(db *gorm.DB, model any, id, tenantID int64) bool {
	err := db.Select("id").Where("id = ? AND tenant_id = ?", id, tenantID).First(model).Error
	return err == nil || !errors.Is(err, gorm.ErrRecordNotFound)
}
