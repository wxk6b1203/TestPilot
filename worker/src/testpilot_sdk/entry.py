"""沙箱入口：python -m testpilot_sdk.entry <source.py> [entry_fn]

由 Worker 的 SubprocessBackend 拉起；控制通道 = stdin(读)/stdout(写)，
stderr 作为用户日志被 Worker 采集。环境变量仅含白名单项（env scrub）。
"""

from __future__ import annotations

import asyncio
import importlib.util
import inspect
import json
import os
import sys
import traceback
from typing import Any


def _apply_rlimits_from_env() -> None:
    """子进程入口应用 Worker 下发的资源限额（替代 preexec_fn，线程安全）。

    TP_SANDBOX_LIMITS 为 JSON：cpu_seconds/mem_mb/max_procs/max_fds/max_fsize_mb。
    必须在本模块 import 任何用户代码前调用；平台不支持/容器已限制时逐项忽略。
    """
    import resource

    raw = os.environ.get("TP_SANDBOX_LIMITS", "")
    if not raw:
        return
    try:
        limits = json.loads(raw)
    except ValueError:
        return

    def _try(what: int, soft: int, hard: int | None = None):
        try:
            resource.setrlimit(what, (soft, hard if hard is not None else soft))
        except (ValueError, OSError):
            pass

    try:
        cpu = max(int(limits.get("cpu_seconds") or 30), 1)
        mem_mb = max(int(limits.get("mem_mb") or 1024), 32)
        procs = max(int(limits.get("max_procs") or 128), 1)
        fds = max(int(limits.get("max_fds") or 128), 16)
        fsize_mb = max(int(limits.get("max_fsize_mb") or 32), 1)
    except (TypeError, ValueError):
        return
    _try(resource.RLIMIT_CPU, cpu, cpu + 5)
    _try(resource.RLIMIT_AS, mem_mb * 1024 * 1024)
    if hasattr(resource, "RLIMIT_NPROC"):
        _try(resource.RLIMIT_NPROC, procs)
    _try(resource.RLIMIT_NOFILE, fds)
    _try(resource.RLIMIT_FSIZE, fsize_mb * 1024 * 1024)


def _load_module(path: str):
    spec = importlib.util.spec_from_file_location("tp_user_case", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load source: {path}")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


async def _amain() -> int:
    _apply_rlimits_from_env()  # 先限额再加载用户代码
    from . import assertions as _assert_mod
    from .bridge import Bridge, set_bridge, set_current_context, clear_current_context
    from .context import Context

    source_path = sys.argv[1]
    entry_name = sys.argv[2] if len(sys.argv) > 2 else "run"

    with open(os.environ["TP_PAYLOAD"], encoding="utf-8") as f:
        payload = json.load(f)

    # 用户的 print() 重定向到 stderr（日志通道），fd 1 保留给桥协议帧
    sys.stdout = sys.stderr  # type: ignore[assignment]

    bridge = Bridge(0, 1)
    bridge.start(asyncio.get_running_loop())
    set_bridge(bridge)

    _assert_mod.reset_records()
    ctx = Context(bridge, payload)

    # 循环模式（行为压测）：迭代前经 iteration_gate 取并发额度（Worker 按负载配置放行/
    # 停止），每次迭代全新 Context（vars 快照），结果以 event 流式上报。
    if payload.get("loop"):
        import time as _time

        fn = getattr(_load_module(source_path), entry_name, None)
        if fn is None:
            raise RuntimeError(f"entry function '{entry_name}' not found in source")

        async def _iteration() -> tuple[bool, str]:
            _assert_mod.reset_records()
            it_ctx = Context(bridge, payload)
            set_current_context(it_ctx)
            try:
                result = fn(it_ctx)
                if inspect.isawaitable(result):
                    await result
                return True, ""
            except AssertionError as e:
                return False, f"assertion failed: {e}"
            except Exception:
                tb = traceback.format_exc(limit=5)
                return False, tb.strip().splitlines()[-1] if tb.strip() else "iteration failed"
            finally:
                clear_current_context()

        iteration = 0
        while True:
            gate = await bridge.call("iteration_gate", {"iteration": iteration})
            if not gate.get("go", True):
                break  # Worker 正常停发（duration 结束）
            iteration += 1
            t0 = _time.perf_counter()
            ok, error = await _iteration()
            elapsed_ms = int((_time.perf_counter() - t0) * 1000)
            bridge.emit({"type": "event", "name": "iteration",
                         "ok": ok, "elapsed_ms": elapsed_ms, "error": error})

        bridge.emit({"type": "result", "ok": True, "error": "",
                     "vars": {}, "assertions": []})
        bridge.close()
        return 0

    ok, error = True, ""
    set_current_context(ctx)
    try:
        fn = getattr(_load_module(source_path), entry_name, None)
        if fn is None:
            raise RuntimeError(f"entry function '{entry_name}' not found in source")
        result = fn(ctx)
        if inspect.isawaitable(result):
            await result
    except AssertionError as e:
        ok, error = False, f"assertion failed: {e}"
    except Exception:
        ok, error = False, traceback.format_exc(limit=8)
    finally:
        clear_current_context()

    bridge.emit({
        "type": "result",
        "ok": ok,
        "error": error,
        "vars": ctx.vars.changed,
        "assertions": _assert_mod.records(),
    })
    bridge.close()
    return 0 if ok else 1


async def _repl_main() -> int:
    """常驻模式（--mode repl，UI 探测 v2）：循环执行 Worker 下发的 exec 帧。

    协议（stdin 帧）：{"type":"exec","id":N,"source":"..."}；
    响应（stdout 帧）：{"type":"done","id":N,"ok":bool,"repr"|"error","logs":[...]}。
    - namespace 跨帧持久（前一次 exec 定义的 helper 可复用）；ctx 每帧新建（vars 快照）；
    - 约定入口 async def run(ctx)；print 经 redirect_stdout 捕获随帧回传；
    - 超时/崩溃由 Worker 侧强杀并重启沙箱（Python 无法打断任意用户代码）。
    """
    _apply_rlimits_from_env()
    import contextlib
    import io
    from . import assertions as _assert_mod
    from .bridge import Bridge, set_bridge, set_current_context, clear_current_context
    from .context import Context

    sys.stdout = sys.stderr  # fd 1 保留协议帧；裸 print（帧外）进日志通道
    q: asyncio.Queue = asyncio.Queue()
    bridge = Bridge(0, 1, push_queue=q)
    bridge.start(asyncio.get_running_loop())
    set_bridge(bridge)

    ns: dict[str, Any] = {"__name__": "tp_probe_repl"}
    while True:
        msg = await q.get()
        call_id = int(msg.get("id", 0))
        source = str(msg.get("source") or "")
        logs_buf = io.StringIO()
        ok, repr_text, err = True, "", ""
        _assert_mod.reset_records()
        ctx = Context(bridge, {"vars": {}, "parameters": {}})
        set_current_context(ctx)
        try:
            ns.pop("run", None)  # 每帧显式定义 run，不继承上一帧的入口
            code = compile(source, "<probe-exec>", "exec")
            exec(code, ns)  # noqa: S102  # 沙箱内受控执行（rlimit+净隔离+env scrub）
            fn = ns.get("run")
            if fn is None or not inspect.iscoroutinefunction(fn):
                raise RuntimeError("probe exec 必须定义 async def run(ctx)")
            with contextlib.redirect_stdout(logs_buf):
                result = await fn(ctx)
            repr_text = repr(result)
        except AssertionError as e:
            ok, err = False, f"assertion failed: {e}"
        except BaseException as e:
            ok, err = False, "".join(traceback.format_exception_only(type(e), e)).strip()
        finally:
            clear_current_context()
        logs = [ln for ln in logs_buf.getvalue().splitlines() if ln.strip()][:200]
        bridge.emit({"type": "done", "id": call_id, "ok": ok,
                     "repr": repr_text if ok else "", "error": err, "logs": logs})


def main() -> None:
    try:
        if "--mode" in sys.argv and sys.argv[sys.argv.index("--mode") + 1] == "repl":
            code = asyncio.run(_repl_main())
            return
        code = asyncio.run(_amain())
    except Exception:
        # 入口自身故障（如桥断裂）：尽力经 stdout 报告
        print("testpilot sdk entry crashed:\n" + traceback.format_exc(limit=5), file=sys.stderr)
        code = 2
    sys.exit(code)


if __name__ == "__main__":
    main()
