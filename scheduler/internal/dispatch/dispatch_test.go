package dispatch

import (
	"path/filepath"
	"testing"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	workerv1 "github.com/testpilot/testpilot/gen/worker/v1"
	"github.com/testpilot/testpilot/internal/db"
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
	if err := d.Register(mkWorker("a", 0, commonv1.Capability_CAPABILITY_FUNCTIONAL)); err != nil {
		t.Fatal(err)
	}
	if got := len(d.Workers()); got != 1 {
		t.Fatalf("workers=%d", got)
	}
	d.Unregister("a")
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
