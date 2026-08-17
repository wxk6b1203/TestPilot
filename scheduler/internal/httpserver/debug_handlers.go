package httpserver

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"
	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/apperr"
	"github.com/testpilot/testpilot/internal/dispatch"
	"github.com/testpilot/testpilot/internal/runner"
	"google.golang.org/protobuf/types/known/durationpb"
)

// debugReq 调试请求 wire 形态（键值数组直传，与 api.ts 约定一致）。
type debugReq struct {
	ProjectID int64  `json:"project_id"`
	APIID     int64  `json:"api_id"`
	Method    int16  `json:"method"`
	URI       string `json:"uri"`
	Params    []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"params"`
	Headers []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"headers"`
	Cookies []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
		Type  string `json:"type"`
	} `json:"cookies"`
	Settings *struct {
		TLSVerify           *bool `json:"tls_verify"`
		FollowRedirects     *bool `json:"follow_redirects"`
		CommentTolerantJSON bool  `json:"comment_tolerant_json"`
		TimeoutMs           int   `json:"timeout_ms"`
	} `json:"settings"`
	Body *struct {
		ContentType int32  `json:"contentType"`
		Raw         string `json:"raw"`
		BinaryRef   string `json:"binary_ref"`
		Form        *struct {
			Fields []struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			} `json:"fields"`
		} `json:"form"`
	} `json:"body"`
	EnvID     int64 `json:"env_id"`
	TimeoutMs int   `json:"timeout_ms"`
}

// debugAPI 执行一次接口调试：构造单步任务派发并同步等待结果。
func (s *Server) debugAPI(ctx fiber.Ctx) error {
	c := claimsOf(ctx)
	var in debugReq
	if !decode(ctx, &in) {
		return nil
	}
	if in.ProjectID == 0 {
		return writeAppErr(ctx, apperr.BadRequest(apperr.CodeInvalidParam, "project_id required"))
	}
	req := runner.DebugRequest{
		ProjectID: in.ProjectID, APIID: in.APIID,
		Method: in.Method, URI: in.URI, EnvID: in.EnvID, TimeoutMs: in.TimeoutMs,
	}
	for _, p := range in.Params {
		req.Params = append(req.Params, &commonv1.KeyValue{Key: p.Key, Value: p.Value})
	}
	for _, h := range in.Headers {
		req.Headers = append(req.Headers, &commonv1.KeyValue{Key: h.Key, Value: h.Value})
	}
	for _, ck := range in.Cookies {
		req.Cookies = append(req.Cookies, &commonv1.CookieParam{Name: ck.Name, Value: ck.Value, Type: ck.Type})
	}
	if in.Settings != nil {
		req.Settings = &commonv1.ApiSettings{CommentTolerantJson: in.Settings.CommentTolerantJSON}
		if in.Settings.TLSVerify != nil {
			req.Settings.TlsVerify = in.Settings.TLSVerify
		}
		if in.Settings.FollowRedirects != nil {
			req.Settings.FollowRedirects = in.Settings.FollowRedirects
		}
		if in.Settings.TimeoutMs > 0 {
			req.Settings.Timeout = durationpb.New(time.Duration(in.Settings.TimeoutMs) * time.Millisecond)
		}
	}
	if in.Body != nil {
		if in.Body.Form != nil && len(in.Body.Form.Fields) > 0 {
			// form-data / x-www-form-urlencoded：oneof 走 FormData 分支
			fd := &commonv1.FormData{}
			for _, f := range in.Body.Form.Fields {
				fd.Fields = append(fd.Fields, &commonv1.FormField{Key: f.Key, Value: f.Value})
			}
			req.Body = &commonv1.BodySpec{Content: &commonv1.BodySpec_Form{Form: fd}}
		} else if in.Body.BinaryRef != "" {
			req.Body = &commonv1.BodySpec{Content: &commonv1.BodySpec_BinaryRef{BinaryRef: in.Body.BinaryRef}}
		} else {
			req.Body = &commonv1.BodySpec{Content: &commonv1.BodySpec_Raw{Raw: in.Body.Raw}}
		}
		if in.Body.ContentType != 0 {
			req.Body.ContentType = commonv1.BodyContentType(in.Body.ContentType)
		}
	}
	res, err := s.run.Debug(ctx.Context(), c.TenantID, req)
	if err != nil {
		if errors.Is(err, dispatch.ErrNoWorker) {
			return writeAppErr(ctx, apperr.Unavailable(apperr.CodeNoWorker, "no suitable worker online"))
		}
		return writeAppErr(ctx, apperr.From(err))
	}
	return writeJSON(ctx, fiber.StatusOK, res)
}
