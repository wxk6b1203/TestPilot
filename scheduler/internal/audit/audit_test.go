package audit

import "testing"

func TestActionOf(t *testing.T) {
	cases := []struct {
		method, path, want string
	}{
		{"POST", "/api/v1/runs/123/run", "run"},
		{"POST", "/api/v1/projects/9/run", "run"},
		{"POST", "/api/v1/projects", "create"},
		{"POST", "/api/v1/runs", "create"},
		{"PUT", "/api/v1/projects/1", "update"},
		{"PATCH", "/api/v1/projects/1", "update"},
		{"DELETE", "/api/v1/projects/1", "delete"},
		{"GET", "/api/v1/projects", "get"},
		{"HEAD", "/api/v1/projects", "head"},
	}
	for _, tc := range cases {
		if got := actionOf(tc.method, tc.path); got != tc.want {
			t.Errorf("actionOf(%q, %q) = %q, want %q", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestResourceType(t *testing.T) {
	cases := []struct {
		path, want string
	}{
		{"/api/v1/tenant/members", "tenant/members"},
		{"/api/v1/tenant/members/5", "tenant/members"},
		{"/api/v1/copilot/sessions", "copilot/sessions"},
		{"/api/v1/copilot/sessions/abc/messages", "copilot/sessions"},
		{"/api/v1/projects", "projects"},
		{"/api/v1/projects/123", "projects"},
		{"/api/v1/tenant", "tenant"},
		{"/api/v1/copilot", "copilot"},
	}
	for _, tc := range cases {
		if got := resourceType(tc.path); got != tc.want {
			t.Errorf("resourceType(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
