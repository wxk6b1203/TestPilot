package httpserver

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestStringifyIDs(t *testing.T) {
	doc := map[string]any{
		"id":         json.Number("123"),
		"project_id": json.Number("456"),
		"tenant_id":  json.Number("789"),
		"count":      json.Number("42"),   // 非 id 键的 number 保持不动
		"name":       "already-string",    // 字符串保持不动
		"note_id":    "999",               // 已是字符串的 id 保持不动
		"nested": map[string]any{
			"id":    json.Number("111"),
			"value": json.Number("222"), // 嵌套非 id 键保持不动
		},
		"list": []any{
			map[string]any{"user_id": json.Number("333")},
			json.Number("444"), // 数组中无键的 number 保持不动
		},
	}
	stringifyIDs(doc)

	if got := doc["id"]; got != "123" {
		t.Errorf(`doc["id"] = %#v, want "123"`, got)
	}
	if got := doc["project_id"]; got != "456" {
		t.Errorf(`doc["project_id"] = %#v, want "456"`, got)
	}
	if got := doc["tenant_id"]; got != "789" {
		t.Errorf(`doc["tenant_id"] = %#v, want "789"`, got)
	}
	if got := doc["count"]; got != json.Number("42") {
		t.Errorf(`doc["count"] = %#v, want json.Number("42")`, got)
	}
	if got := doc["name"]; got != "already-string" {
		t.Errorf(`doc["name"] = %#v, want "already-string"`, got)
	}
	if got := doc["note_id"]; got != "999" {
		t.Errorf(`doc["note_id"] = %#v, want "999"`, got)
	}
	nested := doc["nested"].(map[string]any)
	if got := nested["id"]; got != "111" {
		t.Errorf(`nested["id"] = %#v, want "111"`, got)
	}
	if got := nested["value"]; got != json.Number("222") {
		t.Errorf(`nested["value"] = %#v, want json.Number("222")`, got)
	}
	list := doc["list"].([]any)
	if got := list[0].(map[string]any)["user_id"]; got != "333" {
		t.Errorf(`list[0]["user_id"] = %#v, want "333"`, got)
	}
	if got := list[1]; got != json.Number("444") {
		t.Errorf(`list[1] = %#v, want json.Number("444")`, got)
	}
}

// normalizeIDs 测试用结构体。
type normInner struct {
	ID int64 `json:"id"`
}

type normItem struct {
	ItemID int64 `json:"item_id"`
}

type normTarget struct {
	ProjectID  int64           `json:"project_id"`
	Name       string          `json:"name"`
	Inner      normInner       `json:"inner"`
	Items      []normItem      `json:"items"`
	Definition json.RawMessage `json:"definition"`
}

func TestNormalizeIDs(t *testing.T) {
	doc := map[string]any{
		"project_id": "12345",
		"name":       "not-an-id", // string 字段不受影响
		"inner":      map[string]any{"id": "678"},
		"items": []any{
			map[string]any{"item_id": "901"},
			map[string]any{"item_id": "abc"}, // 非数字字符串保持原样
		},
		// RawMessage 字段内的字符串 ID 保持原样。
		"definition": map[string]any{"id": "111", "project_id": "222"},
	}
	normalizeIDs(reflect.TypeOf(&normTarget{}), doc)

	if got := doc["project_id"]; got != json.Number("12345") {
		t.Errorf(`doc["project_id"] = %#v, want json.Number("12345")`, got)
	}
	if got := doc["name"]; got != "not-an-id" {
		t.Errorf(`doc["name"] = %#v, want "not-an-id"`, got)
	}
	inner := doc["inner"].(map[string]any)
	if got := inner["id"]; got != json.Number("678") {
		t.Errorf(`inner["id"] = %#v, want json.Number("678")`, got)
	}
	items := doc["items"].([]any)
	if got := items[0].(map[string]any)["item_id"]; got != json.Number("901") {
		t.Errorf(`items[0]["item_id"] = %#v, want json.Number("901")`, got)
	}
	if got := items[1].(map[string]any)["item_id"]; got != "abc" {
		t.Errorf(`items[1]["item_id"] = %#v, want "abc"`, got)
	}
	def := doc["definition"].(map[string]any)
	if got := def["id"]; got != "111" {
		t.Errorf(`definition["id"] = %#v, want "111"（RawMessage 内不应还原）`, got)
	}
	if got := def["project_id"]; got != "222" {
		t.Errorf(`definition["project_id"] = %#v, want "222"（RawMessage 内不应还原）`, got)
	}
}

func TestNormalizeIDsNonInt64Targets(t *testing.T) {
	// int64 字段上的非数字字符串保持原样；未出现在结构体中的键保持原样。
	doc := map[string]any{
		"project_id": "12x3",
		"unknown_id": "777",
	}
	normalizeIDs(reflect.TypeOf(&normTarget{}), doc)
	if got := doc["project_id"]; got != "12x3" {
		t.Errorf(`doc["project_id"] = %#v, want "12x3"`, got)
	}
	if got := doc["unknown_id"]; got != "777" {
		t.Errorf(`doc["unknown_id"] = %#v, want "777"`, got)
	}
}
