"""bridge.py：push 帧路由（v2 常驻模式）与既有 op 响应路由互不干扰。"""

import asyncio
import json
import os

from testpilot_sdk.bridge import Bridge


def test_push_frames_routed_to_queue_and_op_responses_unchanged():
    fin_r, fin_w = os.pipe()    # Worker → 沙箱（op 响应 + push 帧）
    fout_r, fout_w = os.pipe()  # 沙箱 → Worker（op 请求/事件）

    async def main():
        q: asyncio.Queue = asyncio.Queue()
        b = Bridge(fin_r, fout_w, push_queue=q)
        b.start(asyncio.get_running_loop())

        # push 帧 → 入队
        os.write(fin_w, json.dumps({"type": "exec", "id": 9, "source": "x"}).encode() + b"\n")
        msg = await asyncio.wait_for(q.get(), 2)
        assert msg["type"] == "exec" and msg["id"] == 9

        # 既有 op 语义：call 发请求帧 → 回写响应 → future 完成
        task = asyncio.create_task(b.call("ui_action", {"action": "click"}))
        reader = asyncio.get_running_loop().run_in_executor(
            None, os.fdopen(os.dup(fout_r), "rb").readline)
        req = json.loads(await asyncio.wait_for(reader, 2))
        assert req["type"] == "op" and req["op"] == "ui_action"
        os.write(fin_w, json.dumps({"id": req["id"], "ok": True, "result": 7}).encode() + b"\n")
        assert await asyncio.wait_for(task, 2) == 7

        # push 仍持续可用（混合帧序）
        os.write(fin_w, json.dumps({"type": "exec", "id": 10, "source": "y"}).encode() + b"\n")
        msg = await asyncio.wait_for(q.get(), 2)
        assert msg["id"] == 10
        b.close()

    asyncio.run(main())
    for fd in (fin_r, fin_w, fout_r, fout_w):
        os.close(fd)
