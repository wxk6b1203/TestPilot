"""client.py：outbox 有界、重复 task_id 拒绝（生命周期防护）。"""

import asyncio

from testpilot.worker.v1 import worker_pb2 as wpb

from testpilot_worker.client import WorkerClient, _OUTBOX_MAX


def test_outbox_is_bounded():
    """outbox 必须有界（调度器消费停滞时无界积压会 OOM Worker）。"""
    c = WorkerClient("127.0.0.1:1", [], 1, [1])
    assert c.outbox.maxsize == _OUTBOX_MAX
    # 满时 _emit 不阻塞、不抛错（丢弃 + 告警），qsize 保持上限
    async def main():
        for _ in range(_OUTBOX_MAX + 64):
            await c._emit(wpb.WorkerEvent(heartbeat=wpb.Heartbeat()))
        assert c.outbox.qsize() <= _OUTBOX_MAX
    asyncio.run(main())


def test_duplicate_task_id_ignored():
    """回归：重复 task_id（调度器重发）不得覆盖既有任务注册——
    覆盖后旧任务失去可取消性（cancel 找不到、断连清理漏掉）。"""
    c = WorkerClient("127.0.0.1:1", [], 1, [1])
    sentinel = object()
    c.tasks["t1"] = sentinel
    task = wpb.TaskAssignment(task_id="t1", run_id="r1")
    asyncio.run(c._run_one(task))
    assert c.tasks.get("t1") is sentinel, "existing task registration must be kept"
    assert len(c.tasks) == 1

