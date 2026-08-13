"""能力桥客户端：沙箱内经 JSON Lines 控制通道与 Worker 通信。

通道分配（由 entry.py 配置）：
- fd 0（stdin）  = Worker → 沙箱 的桥响应
- fd 1（stdout） = 沙箱 → Worker 的桥请求/事件（协议帧）
- fd 2（stderr） = 用户日志（entry 把 sys.stdout 重定向到 stderr，
                   用户 print() 进日志通道，不污染协议）
"""

from __future__ import annotations

import asyncio
import contextvars
import json
import os
import threading
from typing import Any

_bridge_var: contextvars.ContextVar["Bridge | None"] = contextvars.ContextVar("tp_bridge", default=None)


def current_bridge() -> "Bridge":
    b = _bridge_var.get()
    if b is None:
        raise RuntimeError("testpilot-sdk 未在沙箱上下文中运行（bridge 未初始化）")
    return b


def set_bridge(b: "Bridge") -> None:
    _bridge_var.set(b)


class BridgeError(Exception):
    pass


class Bridge:
    """同步 fd + 后台读线程派发，async 调用方经 future 等待。"""

    def __init__(self, fd_r: int = 0, fd_w: int = 1):
        # dup 出来避免 close 时关掉进程真正的 stdin/stdout
        self._r = os.fdopen(os.dup(fd_r), "rb")
        self._w = os.fdopen(os.dup(fd_w), "wb")
        self._write_lock = threading.Lock()
        self._seq = 0
        self._pending: dict[int, asyncio.Future] = {}
        self._loop: asyncio.AbstractEventLoop | None = None
        self._reader = threading.Thread(target=self._read_loop, daemon=True)

    def start(self, loop: asyncio.AbstractEventLoop) -> None:
        self._loop = loop
        self._reader.start()

    def _read_loop(self) -> None:
        for raw in self._r:
            line = raw.strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
            except ValueError:
                continue
            fut = self._pending.pop(int(msg.get("id", -1)), None)
            if fut is None or self._loop is None:
                continue
            def _set(f=fut, m=msg):
                if f.done():
                    return
                if m.get("ok"):
                    f.set_result(m.get("result"))
                else:
                    f.set_exception(BridgeError(str(m.get("error", "bridge call failed"))))
            self._loop.call_soon_threadsafe(_set)

    def _send(self, msg: dict) -> None:
        with self._write_lock:
            self._w.write(json.dumps(msg, ensure_ascii=False).encode() + b"\n")
            self._w.flush()

    async def call(self, op: str, args: dict[str, Any]) -> Any:
        if self._loop is None:
            raise BridgeError("bridge not started")
        self._seq += 1
        call_id = self._seq
        fut: asyncio.Future = self._loop.create_future()
        self._pending[call_id] = fut
        self._send({"type": "op", "id": call_id, "op": op, "args": args})
        return await fut

    def emit(self, msg: dict) -> None:
        """单向事件（log / result），不需要响应。"""
        self._send(msg)

    def close(self) -> None:
        try:
            self._w.close()
        except OSError:
            pass
