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
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/metrics"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// allowPrivateWebhook 允许通知目标为私网/环回地址（内网 webhook 场景）。
// 惰性读取：默认 false——防止租户配置的 webhook URL 被用于 SSRF（Scheduler 打内网/云 metadata）。
func allowPrivateWebhook() bool {
	return os.Getenv("TP_NOTIFY_ALLOW_PRIVATE") == "1"
}

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
	if err := db.Where("tenant_id = ? AND enabled = ?", tenantID, true).Find(&chans).Error; err != nil {
		logging.L.Warnw("notify query channels failed", "tenant", tenantID, "event", event, "err", err)
		return
	}
	for _, ch := range chans {
		if !eventSubscribed(ch.Events, event) {
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

// eventSubscribed 判断逗号分隔的 events 列表是否包含 event（精确匹配，避免子串误配）。
func eventSubscribed(events, event string) bool {
	for _, e := range strings.Split(events, ",") {
		if strings.TrimSpace(e) == event {
			return true
		}
	}
	return false
}

func deliver(ch *model.NotificationChannel, payload map[string]any, title, text string) error {
	// SSRF 防护：仅 http/https + 拒绝私网/环回（默认；TP_NOTIFY_ALLOW_PRIVATE=1 放开内网 webhook）
	if !webhookTargetAllowed(ch.URL) {
		return fmt.Errorf("webhook url not allowed: %q", ch.URL)
	}
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
	_, _ = io.Copy(io.Discard, resp.Body) // 读尽 body：连接可复用
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook status %d", resp.StatusCode)
	}
	return nil
}

// webhookTargetAllowed 校验通知目标 URL：仅 http/https，且（默认）非私网/环回/链路本地。
// 已知残余风险：DNS rebinding TOCTOU——判定与连接是两次独立解析，受控域名可在
// 其间切换（公网→内网）绕过私网拦截。缓解：allow 白名单语义由通知渠道配置者
// 掌控（管理端可见），攻击面为"已配置任意 URL 的管理员"；彻底修复需连接阶段
// 绑定已解析 IP（httpx/Go http 层无标准 hook，改动面大，暂留文档说明）。
func webhookTargetAllowed(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if allowPrivateWebhook() {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	if ip != nil {
		return !(ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())
	}
	// 主机名：解析后任一地址为私网即拒绝；解析失败不拦截（连接阶段自然报错）
	addrs, err := net.LookupHost(u.Hostname())
	if err != nil {
		return true
	}
	for _, a := range addrs {
		if parsed := net.ParseIP(a); parsed != nil &&
			(parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast()) {
			return false
		}
	}
	return true
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
