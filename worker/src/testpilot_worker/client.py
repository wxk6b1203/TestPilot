"""Worker gRPC 客户端：Connect 双向流 — 注册/心跳/收任务/回结果。"""

from __future__ import annotations

import asyncio
import logging
import os
import platform
import socket

import grpc
from google.protobuf import timestamp_pb2

from testpilot.common.v1 import types_pb2 as pb
from testpilot.worker.v1 import worker_pb2 as wpb
from testpilot.worker.v1 import worker_pb2_grpc as wgrpc

from .engine import run_task
from .stress import run_stress
from . import tracing

log = logging.getLogger("testpilot.worker")

SDK_VERSION = "0.1.0"
HEARTBEAT_INTERVAL = 10


class WorkerClient:
    def __init__(self, addr: str, tags: list[str], max_concurrency: int,
                 capabilities: list[int], tenant_id: int = 0):
        self.addr = addr
        self.worker_id = f"{socket.gethostname()}-{os.getpid()}"
        self.tags = tags
        self.max_concurrency = max_concurrency
        self.capabilities = capabilities
        self.tenant_id = tenant_id
        self.sem = asyncio.Semaphore(max_concurrency)
        self.running = 0
        self.outbox: asyncio.Queue[wpb.WorkerEvent] = asyncio.Queue()
        self.tasks: dict[str, asyncio.Task] = {}

    async def run(self):
        """主循环：连接失败无限重连（退避 3s）。"""
        while True:
            try:
                await self._session()
            except grpc.aio.AioRpcError as e:
                log.warning("connection lost: %s %s; retrying in 3s", e.code(), e.details())
            except Exception as e:
                log.exception("session error: %s", e)
            await asyncio.sleep(3)

    async def _session(self):
        async with grpc.aio.insecure_channel(self.addr) as channel:
            stub = wgrpc.WorkerServiceStub(channel)
            log.info("connecting to scheduler at %s ...", self.addr)
            stream = stub.Connect(self._event_stream())
            async for cmd in stream:
                which = cmd.WhichOneof("command")
                if which == "task":
                    asyncio.create_task(self._run_one(cmd.task))
                elif which == "cancel":
                    self._cancel(cmd.cancel.task_id, cmd.cancel.reason)
                elif which == "config":
                    log.info("config update ignored (MVP): %s", cmd.config)

    async def _event_stream(self):
        # 首帧必须是 register
        reg = wpb.RegisterRequest(
            worker_id=self.worker_id,
            worker_name=self.worker_id,
            capabilities=self.capabilities,
            python_version=platform.python_version(),
            sdk_version=SDK_VERSION,
            tags=self.tags,
            max_concurrency=self.max_concurrency,
            tenant_id=self.tenant_id,
        )
        yield wpb.WorkerEvent(register=reg)
        log.info("registered as %s (caps=%s, tags=%s)", self.worker_id, self.capabilities, self.tags)
        hb_task = asyncio.create_task(self._heartbeat_loop())
        try:
            while True:
                yield await self.outbox.get()
        finally:
            hb_task.cancel()

    async def _heartbeat_loop(self):
        while True:
            await asyncio.sleep(HEARTBEAT_INTERVAL)
            hb = wpb.Heartbeat(current_concurrency=self.running)
            hb.ts.CopyFrom(timestamp_pb2.Timestamp())
            hb.ts.GetCurrentTime()
            await self.outbox.put(wpb.WorkerEvent(heartbeat=hb))

    def _cancel(self, task_id: str, reason: str):
        t = self.tasks.pop(task_id, None)
        if t:
            log.info("cancel task %s: %s", task_id, reason)
            t.cancel()

    async def _run_one(self, task: wpb.TaskAssignment):
        # 续链：Scheduler 派发时注入的 traceparent（无则开新 trace）
        parent = tracing.extract_context(task.traceparent)
        with tracing.tracer.start_as_current_span(
            "worker.task", context=parent,
            attributes={"task_id": task.task_id, "run_id": task.run_id,
                        "task_type": task.task_type, "tenant_id": task.tenant_id},
        ):
            await self._run_one_inner(task)

    async def _run_one_inner(self, task: wpb.TaskAssignment):
        async with self.sem:
            self.running += 1
            self.tasks[task.task_id] = asyncio.current_task()
            log.info("task %s start (run=%s, type=%s)", task.task_id, task.run_id, task.task_type)
            try:
                payload = task.WhichOneof("payload")
                if payload == "stress":
                    async def emit_metric(batch: wpb.StressMetricBatch):
                        await self.outbox.put(wpb.WorkerEvent(stress_metrics=batch))
                    result = await run_stress(task, emit_metric)
                elif payload == "functional":
                    async def progress(path: str, status: int, detail: dict):
                        ev = wpb.WorkerEvent(step_progress=wpb.StepProgress(
                            task_id=task.task_id,
                            run_id=task.run_id,
                            case_id=task.functional.case.id,
                            step_path=path,
                            status=status,
                        ))
                        await self.outbox.put(ev)

                    result = await run_task(task, on_progress=progress)
                else:
                    raise NotImplementedError(f"unknown task payload: {payload}")
            except asyncio.CancelledError:
                result = wpb.TaskResult(
                    task_id=task.task_id, run_id=task.run_id,
                    status=pb.RUN_STATUS_ABORTED, error="cancelled")
            except Exception as e:
                log.exception("task %s crashed", task.task_id)
                result = wpb.TaskResult(
                    task_id=task.task_id, run_id=task.run_id,
                    status=pb.RUN_STATUS_FAILED, error=f"worker error: {e}")
            finally:
                self.tasks.pop(task.task_id, None)
                self.running -= 1
            await self.outbox.put(wpb.WorkerEvent(task_result=result))
            log.info("task %s done: status=%s", task.task_id, result.status)
