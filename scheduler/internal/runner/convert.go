package runner

import (
	"encoding/json"
	"fmt"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/logging"
	"github.com/testpilot/testpilot/internal/model"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

func idStr(v int64) string { return fmt.Sprint(v) }

func kvList(raw model.JSON) []*commonv1.KeyValue {
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return nil
	}
	out := make([]*commonv1.KeyValue, 0, len(arr))
	for _, r := range arr {
		kv := &commonv1.KeyValue{}
		if err := protojson.Unmarshal(r, kv); err == nil {
			out = append(out, kv)
		}
	}
	return out
}

func cookieList(raw model.JSON) []*commonv1.CookieParam {
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return nil
	}
	out := make([]*commonv1.CookieParam, 0, len(arr))
	for _, r := range arr {
		c := &commonv1.CookieParam{}
		if err := protojson.Unmarshal(r, c); err == nil {
			out = append(out, c)
		}
	}
	return out
}

func scriptList(raw model.JSON) []*commonv1.Script {
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return nil
	}
	out := make([]*commonv1.Script, 0, len(arr))
	for _, r := range arr {
		s := &commonv1.Script{}
		if err := protojson.Unmarshal(r, s); err == nil {
			out = append(out, s)
		}
	}
	return out
}

func bodySpec(raw model.JSON) *commonv1.BodySpec {
	if len(raw) == 0 {
		return nil
	}
	b := &commonv1.BodySpec{}
	if err := protojson.Unmarshal([]byte(raw), b); err != nil {
		return nil
	}
	return b
}

func apiSettings(raw model.JSON) *commonv1.ApiSettings {
	if len(raw) == 0 {
		return nil
	}
	s := &commonv1.ApiSettings{}
	if err := protojson.Unmarshal([]byte(raw), s); err != nil {
		return nil
	}
	return s
}

// ToProtoHTTP 转换 HttpApi。
func ToProtoHTTP(m *model.HttpApi) *commonv1.HttpApi {
	return &commonv1.HttpApi{
		Id:            idStr(m.ID),
		TenantId:      m.TenantID,
		ProjectId:     idStr(m.ProjectID),
		Method:        commonv1.HttpMethod(m.Method),
		Uri:           m.URI,
		Params:        kvList(m.Params),
		Body:          bodySpec(m.Body),
		Headers:       kvList(m.Headers),
		Cookies:       cookieList(m.Cookies),
		PreScripts:    scriptList(m.PreScripts),
		PostScripts:   scriptList(m.PostScripts),
		Settings:      apiSettings(m.Settings),
		CertificateId: idStr(m.CertificateID),
	}
}

// ToProtoCase 转换 TestCase（declarative 的 steps 存于 Definition）。
func ToProtoCase(m *model.TestCase) *commonv1.TestCase {
	c := &commonv1.TestCase{
		Id:        idStr(m.ID),
		TenantId:  m.TenantID,
		ProjectId: idStr(m.ProjectID),
		Type:      commonv1.TestCaseType(m.Type),
		Name:      m.Name,
	}
	if m.Type == int16(commonv1.TestCaseType_TEST_CASE_TYPE_DECLARATIVE) {
		dc := &commonv1.DeclarativeCase{}
		if err := protojson.Unmarshal([]byte(m.Definition), dc); err == nil {
			c.Definition = &commonv1.TestCase_Declarative{Declarative: dc}
		} else {
			logging.L.Warnw("case definition unmarshal failed", "case_id", m.ID, "err", err)
		}
	} else {
		lc := &commonv1.LowCodeCase{}
		if err := protojson.Unmarshal([]byte(m.Definition), lc); err == nil {
			c.Definition = &commonv1.TestCase_Lowcode{Lowcode: lc}
		} else {
			logging.L.Warnw("case definition unmarshal failed", "case_id", m.ID, "err", err)
		}
	}
	return c
}

// ToProtoVariable 转换 Variable（敏感项只带 secret_ref）。
func ToProtoVariable(m *model.Variable) *commonv1.Variable {
	v := &commonv1.Variable{
		Id:            idStr(m.ID),
		TenantId:      m.TenantID,
		ProjectId:     idStr(m.ProjectID),
		EnvironmentId: idStr(m.EnvironmentID),
		Scope:         commonv1.VariableScope(m.Scope),
		Category:      commonv1.VariableCategory(m.Category),
		Key:           m.Key,
		Sensitive:     m.Sensitive,
		SecretRef:     m.SecretRef,
		Description:   m.Description,
	}
	if !m.Sensitive {
		v.Value = m.Value
	}
	return v
}

// ToProtoEnvironment 转换 Environment。
func ToProtoEnvironment(m *model.Environment) *commonv1.Environment {
	return &commonv1.Environment{
		Id:          idStr(m.ID),
		TenantId:    m.TenantID,
		ProjectId:   idStr(m.ProjectID),
		Icon:        m.Icon,
		Name:        m.Name,
		Description: m.Description,
		BaseUrl:     m.BaseURL,
	}
}

// StructFromMap 构建 structpb（用于 overrides 等）。
func StructFromMap(mp map[string]any) *structpb.Struct {
	s, _ := structpb.NewStruct(mp)
	return s
}
