"""testpilot-sdk：低代码用例 SDK（沙箱内瘦客户端）。

用户脚本只表达编排逻辑；一切副作用（HTTP 调用、变量读写、日志）
经能力桥（IPC）转发给 Worker 执行 —— 沙箱进程无网络、无密钥、环境隔离。
"""

from .assertions import assert_that
from .context import Context
from .models import GrpcAPI, GrpcResponse, HttpAPI, Response

__all__ = ["HttpAPI", "GrpcAPI", "GrpcResponse", "Response", "Context", "assert_that"]
__version__ = "0.1.0"
