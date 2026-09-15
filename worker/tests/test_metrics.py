"""metrics.py：OTel 指标打点（任务收尾/时长/活跃数/outbox 丢弃/探测会话 gauge）。

用 InMemoryMetricReader 注入初始化（无视 TP_OTEL_EXPORTER 开关），断言
instrument 数据点；未初始化场景（no-op 打点）由其余既有测试隐式覆盖。
"""

import asyncio

from opentelemetry.sdk.metrics.export import InMemoryMetricReader

from testpilot.common.v1 import types_pb2 as pb
from testpilot.worker.v1 import worker_pb2 as wpb

from testpilot_worker import metrics
from testpilot_worker.client import WorkerClient, _OUTBOX_MAX

# 模块级共享 reader：get_metrics_data() 读走即清空，测试各自收集互不影响。
_reader = InMemoryMetricReader()
metrics.init(reader=_reader)
# 探测会话 gauge 的采集回调在 WorkerClient 里注入；此处用固定值测试观测路径。
metrics.set_probe_sessions_getter(lambda: 3)


def _snapshot() -> dict:
    """收集当前指标快照：{指标名: {(标签项): 值}}；直方图记 (count, sum)。"""
    out: dict[str, dict] = {}
    data = _reader.get_metrics_data()
    if data is None:
        return out
    for rm in data.resource_metrics:
        for sm in rm.scope_metrics:
            for m in sm.metrics:
                slot = out.setdefault(m.name, {})
                for p in m.data.data_points:
                    labels = tuple(sorted((p.attributes or {}).items()))
                    if hasattr(m.data, "data_points") and hasattr(p, "count"):
                        slot[labels] = (p.count, p.sum)  # HistogramDataPoint
                    else:
                        slot[labels] = slot.get(labels, 0) + p.value
    return out


def test_enum_label_mappings():
    """枚举 → 低基数标签：未知值归 other（防未来新增枚举炸标签基数）。"""
    assert metrics.status_name(pb.RUN_STATUS_PASSED) == "passed"
    assert metrics.status_name(pb.RUN_STATUS_FAILED) == "failed"
    assert metrics.status_name(pb.RUN_STATUS_ABORTED) == "aborted"
    assert metrics.status_name(pb.RUN_STATUS_TIMEOUT) == "timeout"
    assert metrics.status_name(pb.RUN_STATUS_RUNNING) == "running"
    assert metrics.status_name(99) == "other"
    assert metrics.task_type_name(pb.TASK_TYPE_FUNCTIONAL_DECLARATIVE) == "functional_declarative"
    assert metrics.task_type_name(pb.TASK_TYPE_FUNCTIONAL_LOWCODE) == "functional_lowcode"
    assert metrics.task_type_name(pb.TASK_TYPE_PLAYWRIGHT) == "playwright"
    assert metrics.task_type_name(pb.TASK_TYPE_STRESS) == "stress"
    assert metrics.task_type_name(77) == "other"


def test_task_lifecycle_metrics():
    """任务收尾计数/时长/活跃数：未知 payload → status=failed；活跃数净归零。"""
    c = WorkerClient("127.0.0.1:1", [], 1, [1])
    task = wpb.TaskAssignment(task_id="m1", run_id="r1",
                              task_type=pb.TASK_TYPE_FUNCTIONAL_DECLARATIVE)
    asyncio.run(c._run_one(task))
    snap = _snapshot()
    tasks = snap["testpilot.worker.tasks"]
    key = (("status", "failed"), ("task_type", "functional_declarative"))  # 键序=标签排序序
    assert tasks.get(key, 0) >= 1
    dur = snap["testpilot.worker.task.duration"]
    dkey = (("task_type", "functional_declarative"),)
    assert dkey in dur and dur[dkey][0] >= 1 and dur[dkey][1] >= 0
    active = snap["testpilot.worker.active_tasks"]
    assert sum(active.values()) == 0, "任务收尾后活跃任务数必须归零"


def test_outbox_drop_metrics():
    """outbox 满丢弃：结果事件 kind=dropped；心跳腾位牺牲 kind=evicted。"""
    c = WorkerClient("127.0.0.1:1", [], 1, [1])

    async def main():
        for i in range(_OUTBOX_MAX):
            c.outbox.put_nowait(wpb.WorkerEvent(task_result=wpb.TaskResult(task_id=str(i))))
        await c._emit(wpb.WorkerEvent(task_result=wpb.TaskResult(task_id="dropped")))
        await c._emit(wpb.WorkerEvent(heartbeat=wpb.Heartbeat()))

    asyncio.run(main())
    snap = _snapshot()
    drops = snap["testpilot.worker.outbox_dropped"]
    assert drops.get((("kind", "dropped"),), 0) >= 1
    assert drops.get((("kind", "evicted"),), 0) >= 1


def test_probe_sessions_gauge_observed():
    """探测会话 gauge 走注入的 getter 观测（采集时当下读值）。

    前序用例构造 WorkerClient 时会把 getter 覆盖为其真实 hub（空会话），
    故此处重新注入固定值。
    """
    metrics.set_probe_sessions_getter(lambda: 3)
    snap = _snapshot()
    gauge = snap["testpilot.worker.probe_sessions"]
    assert sum(gauge.values()) == 3


def test_task_duration_custom_buckets():
    """回归：task.duration 桶边界为秒刻度定制——SDK 默认桶 (0,5,10,…,10000)
    是毫秒设计，秒记录会全挤进 le=5 桶，分布不可用。"""
    c = WorkerClient("127.0.0.1:1", [], 1, [1])
    task = wpb.TaskAssignment(task_id="b1", run_id="r1",
                              task_type=pb.TASK_TYPE_FUNCTIONAL_DECLARATIVE)
    asyncio.run(c._run_one(task))
    data = _reader.get_metrics_data()
    bounds: list = []
    for rm in data.resource_metrics:
        for sm in rm.scope_metrics:
            for m in sm.metrics:
                if m.name == "testpilot.worker.task.duration":
                    bounds = [list(p.explicit_bounds) for p in m.data.data_points]
    assert bounds and bounds[0] == [1, 5, 10, 30, 60, 120, 300, 600, 1800]
