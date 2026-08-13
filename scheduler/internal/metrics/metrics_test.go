package metrics

import "testing"

func TestRunStatusName(t *testing.T) {
	cases := []struct {
		in   int16
		want string
	}{
		{1, "other"}, // RUNNING 不打点
		{2, "passed"},
		{3, "failed"},
		{4, "canceled"},
		{0, "other"},
		{99, "other"},
		{-1, "other"},
	}
	for _, tc := range cases {
		if got := RunStatusName(tc.in); got != tc.want {
			t.Errorf("RunStatusName(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTriggerName(t *testing.T) {
	cases := []struct {
		in   int16
		want string
	}{
		{1, "manual"},
		{2, "scheduled"},
		{3, "ci"},
		{0, "manual"},
		{99, "manual"},
		{-1, "manual"},
	}
	for _, tc := range cases {
		if got := TriggerName(tc.in); got != tc.want {
			t.Errorf("TriggerName(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestChannelTypeName(t *testing.T) {
	cases := []struct {
		in   int16
		want string
	}{
		{1, "webhook"},
		{2, "dingtalk"},
		{3, "feishu"},
		{0, "webhook"},
		{99, "webhook"},
		{-1, "webhook"},
	}
	for _, tc := range cases {
		if got := ChannelTypeName(tc.in); got != tc.want {
			t.Errorf("ChannelTypeName(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
