package dispatch

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	workerv1 "github.com/testpilot/testpilot/gen/worker/v1"
	"github.com/testpilot/testpilot/internal/db"
	"google.golang.org/protobuf/types/known/durationpb"
	"gorm.io/gorm"
)

// openTestDB：租户 Worker 注册时查 worker_slots 配额，需要真实库（离线 sqlite）。
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "", db.Pool{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mkWorker(id string, tenant int64, caps ...commonv1.Capability) *Worker {
	cs := make([]int32, 0, len(caps))
	for _, c := range caps {
		cs = append(cs, int32(c))
	}
	return &Worker{
		ID:             id,
		TenantID:       tenant,
		Capabilities:   cs,
		MaxConcurrency: 2,
		Send:           make(chan *workerv1.SchedulerCommand, 8),
	}
}

func task(tenantID int64, typ commonv1.TaskType) *workerv1.TaskAssignment {
	return &workerv1.TaskAssignment{TenantId: tenantID, TaskType: typ}
}

func TestRequiredCapability(t *testing.T) {
	cases := map[commonv1.TaskType]commonv1.Capability{
		commonv1.TaskType_TASK_TYPE_FUNCTIONAL_DECLARATIVE: commonv1.Capability_CAPABILITY_FUNCTIONAL,
		commonv1.TaskType_TASK_TYPE_FUNCTIONAL_LOWCODE:     commonv1.Capability_CAPABILITY_LOWCODE,
		commonv1.TaskType_TASK_TYPE_PLAYWRIGHT:             commonv1.Capability_CAPABILITY_PLAYWRIGHT,
		commonv1.TaskType_TASK_TYPE_STRESS:                 commonv1.Capability_CAPABILITY_STRESS,
	}
	for typ, want := range cases {
		if got := requiredCapability(typ); got != want {
			t.Fatalf("%v → %v, want %v", typ, got, want)
		}
	}
}

func TestDispatchLeastLoad(t *testing.T) {
	d := New(openTestDB(t))
	busy := mkWorker("busy", 0, commonv1.Capability_CAPABILITY_FUNCTIONAL)
	idle := mkWorker("idle", 0, commonv1.Capability_CAPABILITY_FUNCTIONAL)
	if err := d.Register(busy); err != nil {
		t.Fatal(err)
	}
	if err := d.Register(idle); err != nil {
		t.Fatal(err)
	}
	d.SetLoad("busy", 1) // MaxConcurrency=2，均未饱和

	if err := d.Dispatch(task(1, commonv1.TaskType_TASK_TYPE_FUNCTIONAL_DECLARATIVE)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-idle.Send: // 应选中更闲的 idle
	case <-busy.Send:
		t.Fatal("picked busy worker over idle one")
	default:
		t.Fatal("nothing sent")
	}
}

func TestDispatchFilters(t *testing.T) {
	d := New(openTestDB(t))
	// 能力不符
	noCap := mkWorker("nocap", 0, commonv1.Capability_CAPABILITY_FUNCTIONAL)
	// 他租户专属
	otherTenant := mkWorker("other", 2, commonv1.Capability_CAPABILITY_LOWCODE)
	// 压测独占中
	inStress := mkWorker("stress", 0, commonv1.Capability_CAPABILITY_LOWCODE)
	inStress.stress.Store(true)
	// 负载饱和
	full := mkWorker("full", 0, commonv1.Capability_CAPABILITY_LOWCODE)
	full.load.Store(2)
	for _, w := range []*Worker{noCap, otherTenant, inStress, full} {
		if err := d.Register(w); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Dispatch(task(1, commonv1.TaskType_TASK_TYPE_FUNCTIONAL_LOWCODE)); err != ErrNoWorker {
		t.Fatalf("want ErrNoWorker, got %v", err)
	}
}

func TestDispatchTenantMatch(t *testing.T) {
	d := New(openTestDB(t))
	dedicated := mkWorker("dedi", 7, commonv1.Capability_CAPABILITY_FUNCTIONAL)
	if err := d.Register(dedicated); err != nil {
		t.Fatal(err)
	}
	// 专属 Worker 接本租户
	if err := d.Dispatch(task(7, commonv1.TaskType_TASK_TYPE_FUNCTIONAL_DECLARATIVE)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dedicated.Send:
	default:
		t.Fatal("dedicated worker should receive own-tenant task")
	}
	// 不接其他租户
	if err := d.Dispatch(task(8, commonv1.TaskType_TASK_TYPE_FUNCTIONAL_DECLARATIVE)); err != ErrNoWorker {
		t.Fatalf("want ErrNoWorker, got %v", err)
	}
}

func TestRegisterTenantQuota(t *testing.T) {
	d := New(openTestDB(t))
	w := mkWorker("a", 0, commonv1.Capability_CAPABILITY_FUNCTIONAL)
	if err := d.Register(w); err != nil {
		t.Fatal(err)
	}
	if got := len(d.Workers()); got != 1 {
		t.Fatalf("workers=%d", got)
	}
	d.Unregister(w)
	if got := len(d.Workers()); got != 0 {
		t.Fatalf("after unregister=%d", got)
	}
}

// 同 ID 重连：旧连接退出的 defer Unregister 只能删自己，不得踢掉新连接。
func TestReconnectDoesNotKickNewWorker(t *testing.T) {
	d := New(openTestDB(t))
	old := mkWorker("a", 0, commonv1.Capability_CAPABILITY_FUNCTIONAL)
	if err := d.Register(old); err != nil {
		t.Fatal(err)
	}
	fresh := mkWorker("a", 0, commonv1.Capability_CAPABILITY_FUNCTIONAL)
	if err := d.Register(fresh); err != nil { // 触发 old.Shutdown，但池里已换新
		t.Fatal(err)
	}
	d.Unregister(old)
	if got := len(d.Workers()); got != 1 {
		t.Fatalf("workers=%d, fresh connection was kicked by stale unregister", got)
	}
	d.Unregister(fresh)
	if got := len(d.Workers()); got != 0 {
		t.Fatalf("after unregister=%d", got)
	}
}

func TestStressWorkers(t *testing.T) {
	d := New(openTestDB(t))
	fn := mkWorker("fn", 0, commonv1.Capability_CAPABILITY_FUNCTIONAL)
	st := mkWorker("st", 0, commonv1.Capability_CAPABILITY_STRESS)
	stOther := mkWorker("st7", 7, commonv1.Capability_CAPABILITY_STRESS)
	for _, w := range []*Worker{fn, st, stOther} {
		if err := d.Register(w); err != nil {
			t.Fatal(err)
		}
	}
	got := d.StressWorkers(1)
	if len(got) != 1 || got[0].ID != "st" {
		t.Fatalf("want [st], got %v", got)
	}
	if got := d.StressWorkers(7); len(got) != 2 { // 共享 st + 专属 st7
		t.Fatalf("tenant 7 want 2, got %d", len(got))
	}
}

// TestDispatchStressCASExcludesConcurrentRuns 回归：DispatchStress 无条件
// Store(true)——两个并发压测互相覆盖独占，同一 Worker 被两场压测同时驱动。
// 修复后独占以 CAS 抢占，失败返回 ErrWorkerBusy（调用方跳过换下一个候选）。
func TestDispatchStressCASExcludesConcurrentRuns(t *testing.T) {
	d := New(openTestDB(t))
	w := mkWorker("st", 0, commonv1.Capability_CAPABILITY_STRESS)
	if err := d.Register(w); err != nil {
		t.Fatal(err)
	}
	task := &workerv1.TaskAssignment{
		TenantId: 1,
		TaskType: commonv1.TaskType_TASK_TYPE_STRESS,
		Timeout:  durationpb.New(time.Minute),
	}

	// 第一场压测独占成功
	if err := d.DispatchStress(w, task); err != nil {
		t.Fatal(err)
	}
	if !w.InStress() {
		t.Fatal("worker should be in stress exclusive")
	}
	// 第二场压测抢占同一 Worker：CAS 失败 → ErrWorkerBusy，独占窗口不被覆盖
	if err := d.DispatchStress(w, task); !errors.Is(err, ErrWorkerBusy) {
		t.Fatalf("second stress on same worker want ErrWorkerBusy, got %v", err)
	}
	// 被独占的 Worker 也不再出现在候选里
	if got := d.StressWorkers(1); len(got) != 0 {
		t.Fatalf("exclusive worker must not be a candidate, got %d", len(got))
	}
	// 解除独占（子任务回报）后可再次派发
	w.EndStress()
	if err := d.DispatchStress(w, task); err != nil {
		t.Fatalf("dispatch after EndStress: %v", err)
	}
}
