#!/usr/bin/env python3
"""TestPilot 联调用 gRPC echo 服务（开启 server reflection，供 GRPC_CALL 反射调用）。

用法：worker/venv/bin/python scripts/grpc_echo_server.py [port]（默认 19090）
"""

import sys
from concurrent import futures

import grpc
from grpc_reflection.v1alpha import reflection

from testpilot.echo.v1 import echo_pb2, echo_pb2_grpc


class EchoService(echo_pb2_grpc.EchoServiceServicer):
    def Echo(self, request, context):
        text = request.message * max(request.repeat, 1)
        return echo_pb2.EchoResponse(message=text, length=len(text))

    def Add(self, request, context):
        return echo_pb2.AddResponse(sum=request.a + request.b)


def main() -> None:
    port = sys.argv[1] if len(sys.argv) > 1 else "19090"
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    echo_pb2_grpc.add_EchoServiceServicer_to_server(EchoService(), server)
    names = (echo_pb2.DESCRIPTOR.services_by_name["EchoService"].full_name,
             reflection.SERVICE_NAME)
    reflection.enable_server_reflection(names, server)
    server.add_insecure_port(f"0.0.0.0:{port}")
    server.start()
    print(f"grpc echo listening on :{port} (reflection enabled)", flush=True)
    server.wait_for_termination()


if __name__ == "__main__":
    main()
