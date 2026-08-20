// Package events 提供单实例进程内的发布/订阅事件总线，用于把 Worker
// 运行进度实时推送给 HTTP SSE 订阅者。
//
// 单实例部署：本实现零外部依赖；多 Scheduler 部署时需替换为 Redis/NATS 等
// 跨实例总线（SSE 连接只订阅本地实例不会丢失单实例事件，但另一实例上的
// Worker 事件不可见——这是当前已知边界）。
package events

import (
	"sync"
	"sync/atomic"
)

// Event 是一条可序列化到 SSE data 帧的事件。
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Broker 按 channel 维护订阅者。channel 形如：
//
//	run:<run_id>    单次功能运行
//	project:<id>    项目维度（运行/压测创建与收尾）
//	stress:<run_id> 单次压测运行
//	workers         在线 Worker 变化
type Broker struct {
	mu   sync.RWMutex
	subs map[string]map[*Subscription]struct{}
}

// Subscription 是单个订阅者的接收端。C 为 64 条缓冲；消费者过慢时丢弃
// 新事件（不阻塞发布方），可用 Dropped() 观察是否发生丢事件。
type Subscription struct {
	C       chan Event
	broker  *Broker
	chans   []string
	dropped atomic.Uint64
}

// NewBroker 构造空总线。
func NewBroker() *Broker {
	return &Broker{subs: map[string]map[*Subscription]struct{}{}}
}

// Subscribe 同时订阅多个 channel；重复 channel 自动去重。
func (b *Broker) Subscribe(channels []string) *Subscription {
	seen := map[string]bool{}
	unique := make([]string, 0, len(channels))
	for _, ch := range channels {
		if ch != "" && !seen[ch] {
			seen[ch] = true
			unique = append(unique, ch)
		}
	}
	s := &Subscription{
		C:      make(chan Event, 64),
		broker: b,
		chans:  unique,
	}
	b.mu.Lock()
	for _, ch := range unique {
		if b.subs[ch] == nil {
			b.subs[ch] = map[*Subscription]struct{}{}
		}
		b.subs[ch][s] = struct{}{}
	}
	b.mu.Unlock()
	return s
}

// Publish 向 channel 的所有订阅者投递事件。无订阅者时直接丢弃。
func (b *Broker) Publish(channel string, e Event) {
	b.mu.RLock()
	set := b.subs[channel]
	b.mu.RUnlock()
	for s := range set {
		select {
		case s.C <- e:
		default:
			s.dropped.Add(1)
		}
	}
}

// SubscriberCount 返回某 channel 当前订阅数（测试/可观测性用）。
func (b *Broker) SubscriberCount(channel string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs[channel])
}

// Close 取消订阅；可重复调用。
func (s *Subscription) Close() {
	if s.broker == nil {
		return
	}
	b := s.broker
	s.broker = nil
	b.mu.Lock()
	for _, ch := range s.chans {
		delete(b.subs[ch], s)
		if len(b.subs[ch]) == 0 {
			delete(b.subs, ch)
		}
	}
	b.mu.Unlock()
}

// Channels 返回订阅的 channel（调试用）。
func (s *Subscription) Channels() []string { return append([]string(nil), s.chans...) }

// Dropped 返回因消费者过慢被丢弃的事件数。
func (s *Subscription) Dropped() uint64 { return s.dropped.Load() }
