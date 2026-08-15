package runner

import (
	"testing"

	"github.com/testpilot/testpilot/internal/model"
)

func TestBodySpecShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		// 断言目标：nil | raw | form；form 时检查字段
		wantKind string
		wantRaw  string
		wantKVs  [][2]string
	}{
		{
			name:     "proto 形状 form 对象",
			raw:      `{"contentType":2,"form":{"fields":[{"key":"a","value":"1"},{"key":"b","value":"2"}]}}`,
			wantKind: "form",
			wantKVs:  [][2]string{{"a", "1"}, {"b", "2"}},
		},
		{
			name:     "旧前端形状 form 数组",
			raw:      `{"contentType":3,"form":[{"key":"x","value":"y"}]}`,
			wantKind: "form",
			wantKVs:  [][2]string{{"x", "y"}},
		},
		{
			name:     "raw",
			raw:      `{"contentType":4,"raw":"{\"k\":1}"}`,
			wantKind: "raw",
			wantRaw:  `{"k":1}`,
		},
		{
			name:     "空",
			raw:      ``,
			wantKind: "nil",
		},
		{
			name:     "非法 JSON",
			raw:      `{"contentType":`,
			wantKind: "nil",
		},
		{
			name:     "oneof 双字段（旧 bug 形态）",
			raw:      `{"contentType":4,"raw":"abc","form":{"fields":[{"key":"a","value":"1"}]}}`,
			wantKind: "nil",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := bodySpec(model.JSON(c.raw))
			switch c.wantKind {
			case "nil":
				if b != nil {
					t.Fatalf("want nil, got %+v", b)
				}
			case "raw":
				if b.GetContent() == nil || b.GetRaw() != c.wantRaw {
					t.Fatalf("want raw %q, got %+v", c.wantRaw, b.GetContent())
				}
			case "form":
				f := b.GetForm()
				if f == nil {
					t.Fatalf("want form, got %+v", b.GetContent())
				}
				if len(f.Fields) != len(c.wantKVs) {
					t.Fatalf("want %d fields, got %d", len(c.wantKVs), len(f.Fields))
				}
				for i, kv := range c.wantKVs {
					if f.Fields[i].Key != kv[0] || f.Fields[i].Value != kv[1] {
						t.Fatalf("field %d: want %v, got %v", i, kv, f.Fields[i])
					}
				}
			}
		})
	}
}
