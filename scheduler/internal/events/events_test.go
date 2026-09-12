package events

import (
	"testing"
	"time"
)

func TestBrokerSubscribePublishClose(t *testing.T) {
	b := NewBroker()
	sub := b.Subscribe([]string{"run:1", "project:2", "run:1"})
	defer sub.Close()
	if len(sub.Channels()) != 2 {
		t.Fatalf("channels=%v", sub.Channels())
	}
	b.Publish("run:1", Event{Type: "step_progress", Data: map[string]any{"ok": true}})
	select {
	case e := <-sub.C:
		if e.Type != "step_progress" {
			t.Fatalf("event=%+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}

	b.Publish("run:9", Event{Type: "nope"})
	select {
	case e := <-sub.C:
		t.Fatalf("unexpected event: %+v", e)
	case <-time.After(20 * time.Millisecond):
	}

	sub.Close()
	b.Publish("run:1", Event{Type: "after_close"})
	select {
	case e := <-sub.C:
		t.Fatalf("event after close: %+v", e)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestBrokerIsolationAndOverflow(t *testing.T) {
	b := NewBroker()
	a := b.Subscribe([]string{"project:1"})
	c := b.Subscribe([]string{"project:2"})
	defer a.Close()
	defer c.Close()

	for i := 0; i < 100; i++ {
		b.Publish("project:1", Event{Type: "tick", Data: i})
	}
	if a.Dropped() == 0 {
		t.Fatal("expected overflow drops")
	}
	// 2 号项目不应收到 1 号项目的事件
	select {
	case e := <-c.C:
		t.Fatalf("cross channel event: %+v", e)
	default:
	}
}

// Publish 与 Subscribe/Close 并发：迭代必须在读锁内，否则
// "concurrent map iteration and map write" 是不可 recover 的 fatal。-race 下必现。
func TestBrokerPublishConcurrentWithSubscribeClose(t *testing.T) {
	b := NewBroker()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2000; i++ {
			ch := []string{"run:" + string(rune('a'+i%8))}
			sub := b.Subscribe(ch)
			b.Publish(ch[0], Event{Type: "tick"})
			sub.Close()
		}
	}()
	for i := 0; i < 2000; i++ {
		b.Publish("run:a", Event{Type: "tick"})
	}
	<-done
}
