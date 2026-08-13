"""Page 模型：低代码沙箱内驱动浏览器。

沙箱无网络出口，所有浏览器操作经能力桥转发给 Worker 的 Playwright UiSession 执行
（op=ui_action）；断言类操作（expect_text/expect_visible）失败时桥返回错误 →
BridgeError → 脚本以失败结束，与声明式 UI_ACTION 语义一致。
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

    async def wait_for(self, milliseconds: int) -> None:
        await self._act("wait", value=str(milliseconds))

    async def screenshot(self, full_page: bool = True) -> dict:
        """截屏（产物挂到用例步骤结果）。"""
        return await self._act("screenshot", value="full" if full_page else "")
