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



def test_full_outbox_heartbeat_evicts_oldest_result_event():
    """回归：outbox 满时心跳必须能驱逐最旧的非心跳事件腾位——心跳被结果事件
    挤掉会让调度器误判 Worker 死亡并重派在途任务（重复执行）。结果类事件
    仍按丢弃策略（reaper 兜底）。"""
    c = WorkerClient("127.0.0.1:1", [], 1, [1])

    async def main():
        for i in range(_OUTBOX_MAX):
            c.outbox.put_nowait(wpb.WorkerEvent(task_result=wpb.TaskResult(task_id=str(i))))
        oldest = c.outbox._queue[0]
        await c._emit(wpb.WorkerEvent(heartbeat=wpb.Heartbeat()))
        assert c.outbox.qsize() == _OUTBOX_MAX  # 腾一位补一位，不突破上限
        events = list(c.outbox._queue)
        assert events[-1].WhichOneof("event") == "heartbeat"  # 新心跳入队
        assert oldest not in events                           # 最旧结果被驱逐
        assert all(e.WhichOneof("event") == "task_result" for e in events[:-1])
        # 结果类事件满时仍被丢弃（策略不变），队尾心跳不受影响
        await c._emit(wpb.WorkerEvent(task_result=wpb.TaskResult(task_id="dropped")))
        assert c.outbox.qsize() == _OUTBOX_MAX
        assert list(c.outbox._queue)[-1].WhichOneof("event") == "heartbeat"
        # 队列全是过期心跳时：旧心跳清出，仅剩新心跳（过期心跳无重排价值）
        while c.outbox.qsize() > 0:
            c.outbox.get_nowait()
        for _ in range(_OUTBOX_MAX):
            c.outbox.put_nowait(wpb.WorkerEvent(heartbeat=wpb.Heartbeat()))
        await c._emit(wpb.WorkerEvent(heartbeat=wpb.Heartbeat()))
        assert c.outbox.qsize() == 1
        assert c.outbox._queue[0].WhichOneof("event") == "heartbeat"

    asyncio.run(main())
