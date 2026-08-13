// Package notify 运行完成通知：webhook / 钉钉 / 飞书。
//
// 渠道存 notification_channels（type: 1=webhook 2=dingtalk 3=feishu）；
// events 逗号分隔订阅（run_finished / stress_finished）；secret 非空时
// 钉钉/飞书按各自规则加签。发送异步、短超时、失败仅记日志。
package notify

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/metrics"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

var client = &http.Client{Timeout: 5 * time.Second}

const (
	EventRunFinished    = "run_finished"
	EventStressFinished = "stress_finished"
)

// RunFinished 功能测试 run 收尾后调用（异步发送）。
func RunFinished(db *gorm.DB, runID int64) {
	var run model.TestRun
	if err := db.First(&run, runID).Error; err != nil {
		return
	}
	payload := map[string]any{
		"event":        EventRunFinished,
		"run_id":       strconv.FormatInt(run.ID, 10),
		"plan_id":      strconv.FormatInt(run.PlanID, 10),
		"status":       run.Status, // 2=passed 3=failed …
		"summary":      json.RawMessage(run.Summary),
		"triggered_by": run.TriggeredBy,
		"finished_at":  run.FinishedAt,
	}
	title := "TestPilot 运行完成"
	text := fmt.Sprintf("计划 %d 运行 %d：status=%d，summary=%s", run.PlanID, run.ID, run.Status, string(run.Summary))
	send(db, run.TenantID, EventRunFinished, payload, title, text)
}

// StressFinished 压测 run 收尾后调用（异步发送）。
func StressFinished(db *gorm.DB, runID int64) {
	var run model.StressRun
	if err := db.First(&run, runID).Error; err != nil {
		return
	}
	payload := map[string]any{
		"event":       EventStressFinished,
		"run_id":      strconv.FormatInt(run.ID, 10),
		"plan_id":     strconv.FormatInt(run.StressPlanID, 10),
		"status":      run.Status,
		"summary":     json.RawMessage(run.Summary),
		"finished_at": run.FinishedAt,
	}
	title := "TestPilot 压测完成"
	text := fmt.Sprintf("压测计划 %d 运行 %d：status=%d，summary=%s", run.StressPlanID, run.ID, run.Status, string(run.Summary))
	send(db, run.TenantID, EventStressFinished, payload, title, text)
}

func send(db *gorm.DB, tenantID int64, event string, payload map[string]any, title, text string) {
	var chans []model.NotificationChannel
	db.Where("tenant_id = ? AND enabled = ?", tenantID, true).Find(&chans)
	for _, ch := range chans {
		if !strings.Contains(ch.Events, event) {
			continue
		}
		ch := ch
		go func() {
			result := "ok"
			if err := deliver(&ch, payload, title, text); err != nil {
				result = "error"
				logging.L.Warnw("notify deliver failed", "channel", ch.ID, "type", ch.Type, "err", err)
			}
			metrics.Notifications.WithLabelValues(metrics.ChannelTypeName(ch.Type), result).Inc()
		}()
	}
}

func deliver(ch *model.NotificationChannel, payload map[string]any, title, text string) error {
	var body []byte
	target := ch.URL
	var err error
	switch ch.Type {
	case 1: // 通用 webhook：原始 JSON
		body, err = json.Marshal(payload)
	case 2: // 钉钉 markdown
		body, err = json.Marshal(map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]string{"title": title, "text": "### " + title + "\n\n" + text},
		})
		if ch.Secret != "" {
			target = dingSign(target, ch.Secret)
		}
	case 3: // 飞书 text
		msg := map[string]any{"msg_type": "text", "content": map[string]string{"text": title + "\n" + text}}
		if ch.Secret != "" {
			ts, sign := feishuSign(ch.Secret)
			msg["timestamp"] = ts
			msg["sign"] = sign
		}
		body, err = json.Marshal(msg)
	default:
		return fmt.Errorf("unknown channel type %d", ch.Type)
	}
	if err != nil {
		return err
	}
	resp, err := client.Post(target, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook status %d", resp.StatusCode)
	}
	return nil
}

// dingSign 钉钉加签：url 追加 timestamp/sign 参数。
func dingSign(rawURL, secret string) string {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "\n" + secret))
	sign := url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	sep := "&"
	if !strings.Contains(rawURL, "?") {
		sep = "?"
	}
	return rawURL + sep + "timestamp=" + ts + "&sign=" + sign
}

// feishuSign 飞书加签：body 内嵌 timestamp/sign。
func feishuSign(secret string) (string, string) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(ts+"\n"+secret))
	mac.Write(nil)
	return ts, base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
