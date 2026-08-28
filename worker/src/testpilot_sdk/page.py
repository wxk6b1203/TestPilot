"""Page 模型：低代码沙箱内驱动浏览器。

沙箱无网络出口，所有浏览器操作经能力桥转发给 Worker 的 Playwright UiSession 执行
（op=ui_action）；断言类操作（expect_text/expect_visible/expect_hidden）失败时
桥返回错误 → BridgeError → 脚本以失败结束，与声明式 UI_ACTION 语义一致。
"""

from __future__ import annotations

from typing import Any

from .bridge import Bridge


class Page:
    def __init__(self, bridge: Bridge):
        self._bridge = bridge

    async def _act(self, action: str, target: str = "", value: str = "") -> Any:
        return await self._bridge.call("ui_action", {
            "action": action, "target": target, "value": value})

    async def _call(self, method: str, *args: Any) -> Any:
        """通用页面调用（op=ui_call）：Worker 侧白名单转发 Playwright（v2 探测）。"""
        r = await self._bridge.call("ui_call", {"method": method, "args": list(args)})
        return r.get("result") if isinstance(r, dict) else r

    # ---- v2 探测方法（机械枚举/状态检查；动作类仍走 _act）----

    async def evaluate(self, expression: str) -> Any:
        """页面上下文执行 JS，返回 JSON 可序列化结果。"""
        return await self._call("evaluate", expression)

    async def content(self) -> str:
        return await self._call("content")

    async def title(self) -> str:
        return await self._call("title")

    async def current_url(self) -> str:
        return await self._call("url")

    async def wait_for_selector(self, selector: str, timeout_ms: int = 15000) -> None:
        await self._call("wait_for_selector", selector, timeout_ms)

    async def aria_snapshot(self) -> str:
        return await self._call("aria_snapshot")

    async def goto(self, url: str) -> None:
        """导航到 url（相对路径基于环境 base_url）。"""
        await self._act("goto", target=url)

    async def click(self, selector: str) -> None:
        await self._act("click", target=selector)

    async def fill(self, selector: str, value: str) -> None:
        await self._act("fill", target=selector, value=value)

    async def select(self, selector: str, value: str) -> None:
        await self._act("select", target=selector, value=value)

    async def check(self, selector: str) -> None:
        await self._act("check", target=selector, value="true")

    async def uncheck(self, selector: str) -> None:
        await self._act("check", target=selector, value="false")

    async def hover(self, selector: str) -> None:
        await self._act("hover", target=selector)

    async def press(self, selector: str, key: str = "Enter") -> None:
        await self._act("press", target=selector, value=key)

    async def expect_text(self, selector: str, text: str) -> None:
        """断言元素文本（不匹配 → 失败）。"""
        await self._act("expect_text", target=selector, value=text)

    async def expect_visible(self, selector: str) -> None:
        await self._act("expect_visible", target=selector)

    async def expect_hidden(self, selector: str) -> None:
        """断言元素不可见（隐藏/不存在）；与声明式 EXPECT_VISIBLE(value=hidden) 等价。"""
        await self._act("expect_visible", target=selector, value="hidden")

    async def wait_for(self, milliseconds: int) -> None:
        if milliseconds < 0:
            raise ValueError("wait_for milliseconds must be >= 0")
        # 桥协议 UI_ACTION_WAIT（无 target）按秒解释；SDK 对用户统一毫秒，
        # 这里换算，否则 wait_for(1000) 会被引擎当成 1000 秒。
        await self._act("wait", value=f"{milliseconds / 1000:g}")

    async def wait_for_selector(self, selector: str,
                                timeout_ms: int = 10_000) -> None:
        """等待 selector 出现；timeout_ms 超时后脚本失败。

        SDK 单位统一为毫秒（桥协议中的 WAIT selector 超时按秒解释，
        这里负责换算，避免调用方理解两套单位）。
        """
        if timeout_ms <= 0:
            raise ValueError("wait_for_selector timeout_ms must be > 0")
        await self._act("wait", target=selector, value=f"{timeout_ms / 1000:g}")

    async def download(self, selector: str, name: str | None = None) -> dict:
        """点击 selector 触发下载并把文件保存为用例产物（name 为空则用响应文件名）。"""
        return await self._act("download", target=selector, value=name or "")

    async def screenshot(self, full_page: bool = True) -> dict:
        """截屏（产物挂到用例步骤结果）。"""
        return await self._act("screenshot", value="full" if full_page else "")
