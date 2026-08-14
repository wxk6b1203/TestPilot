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


def _load_module(path: str):
    spec = importlib.util.spec_from_file_location("tp_user_case", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load source: {path}")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


async def _amain() -> int:
    from . import assertions as _assert_mod
    from .bridge import Bridge, set_bridge
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

    bridge.emit({
        "type": "result",
        "ok": ok,
        "error": error,
        "vars": ctx.vars.changed,
        "assertions": _assert_mod.records(),
    })
    bridge.close()
    return 0 if ok else 1


def main() -> None:
    try:
        code = asyncio.run(_amain())
    except Exception:
        # 入口自身故障（如桥断裂）：尽力经 stdout 报告
        print("testpilot sdk entry crashed:\n" + traceback.format_exc(limit=5), file=sys.stderr)
        code = 2
    sys.exit(code)


if __name__ == "__main__":
    main()
