"""UI 执行器：声明式 UI_ACTION 步骤的 Playwright 实现（Phase 5）。

生命周期：浏览器随首个 UI 步骤 lazy 启动；每用例一个 BrowserContext（隔离），
tracing 全程开启（screenshots+snapshots），用例结束导出 trace.zip + network.har。
产物落盘 TP_ARTIFACT_DIR/<run_id>/<case_result_id>/，ArtifactRef.uri 为相对路径，
由 Scheduler 落库并经 REST 提供下载/预览。
"""

from __future__ import annotations

import os
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Awaitable, Callable

from testpilot.common.v1 import types_pb2 as pb

Render = Callable[[str], str]

_DEFAULT_TIMEOUT_MS = 10_000
_EXPECT_TIMEOUT_MS = 5_000


class UiUnavailable(Exception):
    """Worker 未安装 playwright 变体。"""


@dataclass
class UiArtifact:
    kind: str  # screenshot | trace | har | download
    uri: str   # 相对 artifact 根的路径
    size: int


@dataclass
class UiSession:
    base_url: str
    case_dir: Path          # 本用例产物目录（绝对）
    case_rel: str           # 本用例产物目录（相对 artifact 根，用于 uri）
    render: Render          # {{var}} 模板渲染

    _pw: Any = None
    _browser: Any = None
    _ctx: Any = None
    page: Any = None
    _seq: int = 0
    downloads: list[UiArtifact] = field(default_factory=list)

    async def ensure(self) -> None:
        if self.page is not None:
            return
        try:
            from playwright.async_api import async_playwright
        except ImportError as e:
            raise UiUnavailable(
                "playwright 未安装：uv pip install 'testpilot-worker[playwright]' "
                "&& playwright install chromium") from e
        self.case_dir.mkdir(parents=True, exist_ok=True)
        self._pw = await async_playwright().start()
        # channel=chromium：用完整 Chromium 跑 headless（兼容未装 headless_shell 的环境，
        # 标准 playwright install chromium 两种都装，行为一致）
        self._browser = await self._pw.chromium.launch(headless=True, channel="chromium")
        self._ctx = await self._browser.new_context(
            base_url=self.base_url or None,
            record_har_path=str(self.case_dir / "network.har"),
            record_har_content="omit",
        )
        await self._ctx.tracing.start(screenshots=True, snapshots=True)
        self.page = await self._ctx.new_page()
        self.page.set_default_timeout(_DEFAULT_TIMEOUT_MS)

    def _artifact(self, kind: str, path: Path) -> UiArtifact:
        size = path.stat().st_size if path.exists() else 0
        return UiArtifact(kind=kind, uri=f"{self.case_rel}/{path.name}", size=size)

    async def execute(self, action: int, target: str, value: str,
                      logs: list[str]) -> list[UiArtifact]:
        """执行单个 UI_ACTION，返回本步骤产物。"""
        await self.ensure()
        page = self.page
        target, value = self.render(target), self.render(value)
        arts: list[UiArtifact] = []

        if action == pb.UI_ACTION_GOTO:
            url = value or target
            # SSRF 出口校验：浏览器是第二个出网通道，必须与 http_exec 同策略；
            # 拒绝 file:// 等非 http(s) scheme（本地文件外带面）
            from urllib.parse import urlparse
            if urlparse(url).scheme not in ("", "http", "https"):
                raise ValueError(f"goto scheme not allowed: {urlparse(url).scheme!r}")
            full = url if urlparse(url).scheme else self.base_url.rstrip("/") + "/" + url.lstrip("/")
            from . import egress
            await egress.acheck_url(full)
            resp = await page.goto(url, wait_until="domcontentloaded")
            logs.append(f"goto {url} -> {resp.status if resp else '?'}")
        elif action == pb.UI_ACTION_CLICK:
            await page.click(target)
            logs.append(f"click {target}")
        elif action == pb.UI_ACTION_FILL:
            await page.fill(target, value)
            logs.append(f"fill {target} = {value!r}")
        elif action == pb.UI_ACTION_SELECT:
            await page.select_option(target, value)
            logs.append(f"select {target} = {value!r}")
        elif action == pb.UI_ACTION_CHECK:
            if value.lower() in ("false", "0", "off", "uncheck"):
                await page.uncheck(target)
                logs.append(f"uncheck {target}")
            else:
                await page.check(target)
                logs.append(f"check {target}")
        elif action == pb.UI_ACTION_HOVER:
            await page.hover(target)
            logs.append(f"hover {target}")
        elif action == pb.UI_ACTION_PRESS:
            if target:
                await page.press(target, value or "Enter")
                logs.append(f"press {value or 'Enter'} on {target}")
            else:
                await page.keyboard.press(value or "Enter")
                logs.append(f"keyboard press {value or 'Enter'}")
        elif action == pb.UI_ACTION_EXPECT_TEXT:
            from playwright.async_api import expect
            loc = page.locator(target or "body")
            await expect(loc).to_contain_text(value, timeout=_EXPECT_TIMEOUT_MS)
            logs.append(f"expect_text {target or 'body'} contains {value!r}")
        elif action == pb.UI_ACTION_EXPECT_VISIBLE:
            from playwright.async_api import expect
            loc = page.locator(target)
            if value.lower() in ("hidden", "false", "0"):
                await expect(loc).to_be_hidden(timeout=_EXPECT_TIMEOUT_MS)
                logs.append(f"expect_hidden {target}")
            else:
                await expect(loc).to_be_visible(timeout=_EXPECT_TIMEOUT_MS)
                logs.append(f"expect_visible {target}")
        elif action == pb.UI_ACTION_SCREENSHOT:
            self._seq += 1
            if value and not value.lower() in ("full", "true", "1"):
                name = _safe_artifact_name(value, f"shot-{self._seq}.png")
            else:
                name = f"shot-{self._seq}.png"
            if not name.endswith(".png"):
                name += ".png"
            path = self.case_dir / name
            await page.screenshot(path=str(path), full_page=value.lower() in ("full", "true", "1"))
            arts.append(self._artifact("screenshot", path))
            logs.append(f"screenshot -> {path.name}")
        elif action == pb.UI_ACTION_WAIT:
            if target:
                await page.wait_for_selector(
                    target, timeout=float(value) * 1000 if value else _DEFAULT_TIMEOUT_MS)
                logs.append(f"wait_for {target}")
            else:
                ms = int(float(value or "1") * 1000)
                await page.wait_for_timeout(ms)
                logs.append(f"wait {ms}ms")
        elif action == pb.UI_ACTION_UPLOAD:
            # 路径穿越/任意文件读取防护：仅允许相对路径且不含 ..（本机文件上传
            # 是任意文件读取外带面——值必须落在 artifact 根内）
            if value.startswith("/") or ".." in value.split("/"):
                raise ValueError(f"upload path not allowed: {value!r}")
            await page.set_input_files(target, value)
            logs.append(f"upload {value} -> {target}")
        elif action == pb.UI_ACTION_DOWNLOAD:
            async with page.expect_download() as dl_info:
                await page.click(target)
            download = await dl_info.value
            # suggested_filename 由被测服务器 Content-Disposition 控制——必须净化防穿越
            fname = _safe_artifact_name(value or download.suggested_filename or "",
                                        f"download-{self._seq}")
            path = self.case_dir / fname
            await download.save_as(str(path))
            arts.append(self._artifact("download", path))
            logs.append(f"download -> {fname}")
        else:
            raise ValueError(f"unknown ui action: {action}")
        return arts

    async def failure_screenshot(self) -> list[UiArtifact]:
        """步骤失败时的现场快照（尽力而为）。"""
        if self.page is None:
            return []
        try:
            self._seq += 1
            path = self.case_dir / f"failure-{self._seq}.png"
            await self.page.screenshot(path=str(path), full_page=True)
            return [self._artifact("screenshot", path)]
        except Exception:
            return []

    async def finish(self) -> list[UiArtifact]:
        """用例结束：导出 trace/har，关闭浏览器。幂等。"""
        arts: list[UiArtifact] = []
        if self._ctx is not None:
            try:
                trace_path = self.case_dir / "trace.zip"
                await self._ctx.tracing.stop(path=str(trace_path))
                arts.append(self._artifact("trace", trace_path))
            except Exception:
                pass
            try:
                await self._ctx.close()  # 关闭时落盘 har
                har = self.case_dir / "network.har"
                if har.exists():
                    arts.append(self._artifact("har", har))
            except Exception:
                pass
        if self._browser is not None:
            try:
                await self._browser.close()
            except Exception:
                pass
        if self._pw is not None:
            try:
                await self._pw.stop()
            except Exception:
                pass
        self.page = self._ctx = self._browser = self._pw = None
        return arts


def artifact_root() -> Path:
    return Path(os.environ.get("TP_ARTIFACT_DIR", ".data/artifacts"))


def sanitize(name: str) -> str:
    """产物文件名净化：先剥路径成分（含 ../ 与绝对路径），再字符白名单。

    原名直接拼 case_dir 可被 value/suggested_filename 携带的 `../` 或绝对路径
    穿越写出 artifact 根（任意路径写文件）——这是 P0 安全缺陷。
    """
    base = str(name).replace("\\", "/").rsplit("/", 1)[-1]
    safe = re.sub(r"[^A-Za-z0-9_.-]+", "_", base)[:80].strip(".")
    return safe or "artifact"


def _safe_artifact_name(raw: str, fallback: str) -> str:
    """净化后的文件名；空/非法时回退 fallback（确保可写且不逃逸）。"""
    safe = sanitize(raw)
    if safe in (".", "..", ""):
        return fallback
    return safe

# ---- 能力桥 UI 操作（低代码 Page 模型：沙箱 → Worker 转发 Playwright）----

BRIDGE_UI_ACTIONS = {
    "goto": pb.UI_ACTION_GOTO, "click": pb.UI_ACTION_CLICK, "fill": pb.UI_ACTION_FILL,
    "select": pb.UI_ACTION_SELECT, "check": pb.UI_ACTION_CHECK, "hover": pb.UI_ACTION_HOVER,
    "press": pb.UI_ACTION_PRESS, "expect_text": pb.UI_ACTION_EXPECT_TEXT,
    "expect_visible": pb.UI_ACTION_EXPECT_VISIBLE, "screenshot": pb.UI_ACTION_SCREENSHOT,
    "wait": pb.UI_ACTION_WAIT, "upload": pb.UI_ACTION_UPLOAD, "download": pb.UI_ACTION_DOWNLOAD,
}


def bridge_ui_handler(get_session) -> Callable[[dict], Awaitable[dict]]:
    """op=ui_action 的桥处理器：args {action, target, value} → UiSession.execute。
    get_session 返回本用例的 UiSession（惰性创建，产物目录按 run/case 隔离）。"""

    async def handle(args: dict) -> dict:
        action = str(args.get("action") or "")
        kind = BRIDGE_UI_ACTIONS.get(action)
        if kind is None:
            raise ValueError(f"unknown ui action: {action!r}")
        logs: list[str] = []
        arts = await get_session().execute(
            kind, str(args.get("target") or ""), str(args.get("value") or ""), logs)
        return {
            "logs": logs,
            "artifacts": [{"kind": a.kind, "uri": a.uri, "size": a.size} for a in arts],
        }

    return handle
