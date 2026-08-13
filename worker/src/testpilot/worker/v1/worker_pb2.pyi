import datetime

from google.protobuf import duration_pb2 as _duration_pb2
from google.protobuf import struct_pb2 as _struct_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from testpilot.common.v1 import types_pb2 as _types_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class WorkerEvent(_message.Message):
    __slots__ = ("register", "heartbeat", "step_progress", "log_batch", "task_result", "stress_metrics", "artifact")
    REGISTER_FIELD_NUMBER: _ClassVar[int]
    HEARTBEAT_FIELD_NUMBER: _ClassVar[int]
    STEP_PROGRESS_FIELD_NUMBER: _ClassVar[int]
    LOG_BATCH_FIELD_NUMBER: _ClassVar[int]
    TASK_RESULT_FIELD_NUMBER: _ClassVar[int]
    STRESS_METRICS_FIELD_NUMBER: _ClassVar[int]
    ARTIFACT_FIELD_NUMBER: _ClassVar[int]
    register: RegisterRequest
    heartbeat: Heartbeat
    step_progress: StepProgress
    log_batch: LogBatch
    task_result: TaskResult
    stress_metrics: StressMetricBatch
    artifact: _types_pb2.ArtifactRef
    def __init__(self, register: _Optional[_Union[RegisterRequest, _Mapping]] = ..., heartbeat: _Optional[_Union[Heartbeat, _Mapping]] = ..., step_progress: _Optional[_Union[StepProgress, _Mapping]] = ..., log_batch: _Optional[_Union[LogBatch, _Mapping]] = ..., task_result: _Optional[_Union[TaskResult, _Mapping]] = ..., stress_metrics: _Optional[_Union[StressMetricBatch, _Mapping]] = ..., artifact: _Optional[_Union[_types_pb2.ArtifactRef, _Mapping]] = ...) -> None: ...

class RegisterRequest(_message.Message):
    __slots__ = ("worker_id", "worker_name", "capabilities", "python_version", "sdk_version", "worker_version", "tags", "max_concurrency", "tenant_id")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    WORKER_NAME_FIELD_NUMBER: _ClassVar[int]
    CAPABILITIES_FIELD_NUMBER: _ClassVar[int]
    PYTHON_VERSION_FIELD_NUMBER: _ClassVar[int]
    SDK_VERSION_FIELD_NUMBER: _ClassVar[int]
    WORKER_VERSION_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    MAX_CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    worker_id: str
    worker_name: str
    capabilities: _containers.RepeatedScalarFieldContainer[_types_pb2.Capability]
    python_version: str
    sdk_version: str
    worker_version: str
    tags: _containers.RepeatedScalarFieldContainer[str]
    max_concurrency: int
    tenant_id: int
    def __init__(self, worker_id: _Optional[str] = ..., worker_name: _Optional[str] = ..., capabilities: _Optional[_Iterable[_Union[_types_pb2.Capability, str]]] = ..., python_version: _Optional[str] = ..., sdk_version: _Optional[str] = ..., worker_version: _Optional[str] = ..., tags: _Optional[_Iterable[str]] = ..., max_concurrency: _Optional[int] = ..., tenant_id: _Optional[int] = ...) -> None: ...

class Heartbeat(_message.Message):
    __slots__ = ("current_concurrency", "ts")
    CURRENT_CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    TS_FIELD_NUMBER: _ClassVar[int]
    current_concurrency: int
    ts: _timestamp_pb2.Timestamp
    def __init__(self, current_concurrency: _Optional[int] = ..., ts: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class StepProgress(_message.Message):
    __slots__ = ("task_id", "run_id", "case_id", "step_path", "status", "detail")
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    CASE_ID_FIELD_NUMBER: _ClassVar[int]
    STEP_PATH_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    DETAIL_FIELD_NUMBER: _ClassVar[int]
    task_id: str
    run_id: str
    case_id: str
    step_path: str
    status: _types_pb2.StepStatus
    detail: _struct_pb2.Struct
    def __init__(self, task_id: _Optional[str] = ..., run_id: _Optional[str] = ..., case_id: _Optional[str] = ..., step_path: _Optional[str] = ..., status: _Optional[_Union[_types_pb2.StepStatus, str]] = ..., detail: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ...) -> None: ...

class LogBatch(_message.Message):
    __slots__ = ("task_id", "run_id", "case_id", "step_path", "lines")
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    CASE_ID_FIELD_NUMBER: _ClassVar[int]
    STEP_PATH_FIELD_NUMBER: _ClassVar[int]
    LINES_FIELD_NUMBER: _ClassVar[int]
    task_id: str
    run_id: str
    case_id: str
    step_path: str
    lines: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, task_id: _Optional[str] = ..., run_id: _Optional[str] = ..., case_id: _Optional[str] = ..., step_path: _Optional[str] = ..., lines: _Optional[_Iterable[str]] = ...) -> None: ...

class TaskResult(_message.Message):
    __slots__ = ("task_id", "run_id", "status", "error", "case_results", "step_results", "duration")
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    CASE_RESULTS_FIELD_NUMBER: _ClassVar[int]
    STEP_RESULTS_FIELD_NUMBER: _ClassVar[int]
    DURATION_FIELD_NUMBER: _ClassVar[int]
    task_id: str
    run_id: str
    status: _types_pb2.RunStatus
    error: str
    case_results: _containers.RepeatedCompositeFieldContainer[_types_pb2.TestCaseResult]
    step_results: _containers.RepeatedCompositeFieldContainer[_types_pb2.TestStepResult]
    duration: _duration_pb2.Duration
    def __init__(self, task_id: _Optional[str] = ..., run_id: _Optional[str] = ..., status: _Optional[_Union[_types_pb2.RunStatus, str]] = ..., error: _Optional[str] = ..., case_results: _Optional[_Iterable[_Union[_types_pb2.TestCaseResult, _Mapping]]] = ..., step_results: _Optional[_Iterable[_Union[_types_pb2.TestStepResult, _Mapping]]] = ..., duration: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ...) -> None: ...

class StressMetricBatch(_message.Message):
    __slots__ = ("task_id", "run_id", "points")
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    POINTS_FIELD_NUMBER: _ClassVar[int]
    task_id: str
    run_id: str
    points: _containers.RepeatedCompositeFieldContainer[_types_pb2.StressMetricPoint]
    def __init__(self, task_id: _Optional[str] = ..., run_id: _Optional[str] = ..., points: _Optional[_Iterable[_Union[_types_pb2.StressMetricPoint, _Mapping]]] = ...) -> None: ...

class SchedulerCommand(_message.Message):
    __slots__ = ("task", "cancel", "config")
    TASK_FIELD_NUMBER: _ClassVar[int]
    CANCEL_FIELD_NUMBER: _ClassVar[int]
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    task: TaskAssignment
    cancel: CancelTask
    config: ConfigUpdate
    def __init__(self, task: _Optional[_Union[TaskAssignment, _Mapping]] = ..., cancel: _Optional[_Union[CancelTask, _Mapping]] = ..., config: _Optional[_Union[ConfigUpdate, _Mapping]] = ...) -> None: ...

class ExecutionEnv(_message.Message):
    __slots__ = ("environment", "variables", "base_url")
    ENVIRONMENT_FIELD_NUMBER: _ClassVar[int]
    VARIABLES_FIELD_NUMBER: _ClassVar[int]
    BASE_URL_FIELD_NUMBER: _ClassVar[int]
    environment: _types_pb2.Environment
    variables: _containers.RepeatedCompositeFieldContainer[_types_pb2.Variable]
    base_url: str
    def __init__(self, environment: _Optional[_Union[_types_pb2.Environment, _Mapping]] = ..., variables: _Optional[_Iterable[_Union[_types_pb2.Variable, _Mapping]]] = ..., base_url: _Optional[str] = ...) -> None: ...

class FunctionalTask(_message.Message):
    __slots__ = ("case", "case_result_id", "grpc_apis")
    class GrpcApisEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: _types_pb2.GrpcApi
        def __init__(self, key: _Optional[str] = ..., value: _Optional[_Union[_types_pb2.GrpcApi, _Mapping]] = ...) -> None: ...
    CASE_FIELD_NUMBER: _ClassVar[int]
    CASE_RESULT_ID_FIELD_NUMBER: _ClassVar[int]
    GRPC_APIS_FIELD_NUMBER: _ClassVar[int]
    case: _types_pb2.TestCase
    case_result_id: str
    grpc_apis: _containers.MessageMap[str, _types_pb2.GrpcApi]
    def __init__(self, case: _Optional[_Union[_types_pb2.TestCase, _Mapping]] = ..., case_result_id: _Optional[str] = ..., grpc_apis: _Optional[_Mapping[str, _types_pb2.GrpcApi]] = ...) -> None: ...

class StressTask(_message.Message):
    __slots__ = ("plan", "worker_index", "assigned_concurrency", "metrics_label", "inline_api")
    PLAN_FIELD_NUMBER: _ClassVar[int]
    WORKER_INDEX_FIELD_NUMBER: _ClassVar[int]
    ASSIGNED_CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    METRICS_LABEL_FIELD_NUMBER: _ClassVar[int]
    INLINE_API_FIELD_NUMBER: _ClassVar[int]
    plan: _types_pb2.StressTestPlan
    worker_index: int
    assigned_concurrency: int
    metrics_label: str
    inline_api: _types_pb2.HttpApi
    def __init__(self, plan: _Optional[_Union[_types_pb2.StressTestPlan, _Mapping]] = ..., worker_index: _Optional[int] = ..., assigned_concurrency: _Optional[int] = ..., metrics_label: _Optional[str] = ..., inline_api: _Optional[_Union[_types_pb2.HttpApi, _Mapping]] = ...) -> None: ...

class TaskAssignment(_message.Message):
    __slots__ = ("task_id", "run_id", "tenant_id", "task_type", "timeout", "functional", "stress", "env", "traceparent")
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    TASK_TYPE_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    FUNCTIONAL_FIELD_NUMBER: _ClassVar[int]
    STRESS_FIELD_NUMBER: _ClassVar[int]
    ENV_FIELD_NUMBER: _ClassVar[int]
    TRACEPARENT_FIELD_NUMBER: _ClassVar[int]
    task_id: str
    run_id: str
    tenant_id: int
    task_type: _types_pb2.TaskType
    timeout: _duration_pb2.Duration
    functional: FunctionalTask
    stress: StressTask
    env: ExecutionEnv
    traceparent: str
    def __init__(self, task_id: _Optional[str] = ..., run_id: _Optional[str] = ..., tenant_id: _Optional[int] = ..., task_type: _Optional[_Union[_types_pb2.TaskType, str]] = ..., timeout: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., functional: _Optional[_Union[FunctionalTask, _Mapping]] = ..., stress: _Optional[_Union[StressTask, _Mapping]] = ..., env: _Optional[_Union[ExecutionEnv, _Mapping]] = ..., traceparent: _Optional[str] = ...) -> None: ...

class CancelTask(_message.Message):
    __slots__ = ("task_id", "reason")
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    task_id: str
    reason: str
    def __init__(self, task_id: _Optional[str] = ..., reason: _Optional[str] = ...) -> None: ...

class ConfigUpdate(_message.Message):
    __slots__ = ("config",)
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    config: _struct_pb2.Struct
    def __init__(self, config: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ...) -> None: ...
