from testpilot.common.v1 import types_pb2 as _types_pb2
from google.protobuf import struct_pb2 as _struct_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ApiKind(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    API_KIND_UNSPECIFIED: _ClassVar[ApiKind]
    API_KIND_HTTP: _ClassVar[ApiKind]
    API_KIND_GRPC: _ClassVar[ApiKind]
API_KIND_UNSPECIFIED: ApiKind
API_KIND_HTTP: ApiKind
API_KIND_GRPC: ApiKind

class ListProjectsRequest(_message.Message):
    __slots__ = ("ctx", "page", "query")
    CTX_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    QUERY_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    page: _types_pb2.PageRequest
    query: str
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., page: _Optional[_Union[_types_pb2.PageRequest, _Mapping]] = ..., query: _Optional[str] = ...) -> None: ...

class ListProjectsResponse(_message.Message):
    __slots__ = ("projects", "page")
    PROJECTS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    projects: _containers.RepeatedCompositeFieldContainer[_types_pb2.Project]
    page: _types_pb2.PageResponse
    def __init__(self, projects: _Optional[_Iterable[_Union[_types_pb2.Project, _Mapping]]] = ..., page: _Optional[_Union[_types_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class ListApisRequest(_message.Message):
    __slots__ = ("ctx", "project_id", "page", "query")
    CTX_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    QUERY_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    project_id: str
    page: _types_pb2.PageRequest
    query: str
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., project_id: _Optional[str] = ..., page: _Optional[_Union[_types_pb2.PageRequest, _Mapping]] = ..., query: _Optional[str] = ...) -> None: ...

class ListApisResponse(_message.Message):
    __slots__ = ("http_apis", "grpc_apis", "page")
    HTTP_APIS_FIELD_NUMBER: _ClassVar[int]
    GRPC_APIS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    http_apis: _containers.RepeatedCompositeFieldContainer[_types_pb2.HttpApi]
    grpc_apis: _containers.RepeatedCompositeFieldContainer[_types_pb2.GrpcApi]
    page: _types_pb2.PageResponse
    def __init__(self, http_apis: _Optional[_Iterable[_Union[_types_pb2.HttpApi, _Mapping]]] = ..., grpc_apis: _Optional[_Iterable[_Union[_types_pb2.GrpcApi, _Mapping]]] = ..., page: _Optional[_Union[_types_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class GetApiRequest(_message.Message):
    __slots__ = ("ctx", "api_id", "kind")
    CTX_FIELD_NUMBER: _ClassVar[int]
    API_ID_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    api_id: str
    kind: ApiKind
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., api_id: _Optional[str] = ..., kind: _Optional[_Union[ApiKind, str]] = ...) -> None: ...

class GetApiResponse(_message.Message):
    __slots__ = ("http", "grpc")
    HTTP_FIELD_NUMBER: _ClassVar[int]
    GRPC_FIELD_NUMBER: _ClassVar[int]
    http: _types_pb2.HttpApi
    grpc: _types_pb2.GrpcApi
    def __init__(self, http: _Optional[_Union[_types_pb2.HttpApi, _Mapping]] = ..., grpc: _Optional[_Union[_types_pb2.GrpcApi, _Mapping]] = ...) -> None: ...

class ListEnvironmentsRequest(_message.Message):
    __slots__ = ("ctx", "project_id")
    CTX_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    project_id: str
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., project_id: _Optional[str] = ...) -> None: ...

class ListEnvironmentsResponse(_message.Message):
    __slots__ = ("environments",)
    ENVIRONMENTS_FIELD_NUMBER: _ClassVar[int]
    environments: _containers.RepeatedCompositeFieldContainer[_types_pb2.Environment]
    def __init__(self, environments: _Optional[_Iterable[_Union[_types_pb2.Environment, _Mapping]]] = ...) -> None: ...

class ListTestCasesRequest(_message.Message):
    __slots__ = ("ctx", "project_id", "page", "query", "tags")
    CTX_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    QUERY_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    project_id: str
    page: _types_pb2.PageRequest
    query: str
    tags: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., project_id: _Optional[str] = ..., page: _Optional[_Union[_types_pb2.PageRequest, _Mapping]] = ..., query: _Optional[str] = ..., tags: _Optional[_Iterable[str]] = ...) -> None: ...

class ListTestCasesResponse(_message.Message):
    __slots__ = ("cases", "page")
    CASES_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    cases: _containers.RepeatedCompositeFieldContainer[_types_pb2.TestCase]
    page: _types_pb2.PageResponse
    def __init__(self, cases: _Optional[_Iterable[_Union[_types_pb2.TestCase, _Mapping]]] = ..., page: _Optional[_Union[_types_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class GetTestCaseRequest(_message.Message):
    __slots__ = ("ctx", "case_id")
    CTX_FIELD_NUMBER: _ClassVar[int]
    CASE_ID_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    case_id: str
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., case_id: _Optional[str] = ...) -> None: ...

class GetTestCaseResponse(_message.Message):
    __slots__ = ("case",)
    CASE_FIELD_NUMBER: _ClassVar[int]
    case: _types_pb2.TestCase
    def __init__(self, case: _Optional[_Union[_types_pb2.TestCase, _Mapping]] = ...) -> None: ...

class QuerySchemaRequest(_message.Message):
    __slots__ = ("ctx", "topic")
    CTX_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    topic: str
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., topic: _Optional[str] = ...) -> None: ...

class QuerySchemaResponse(_message.Message):
    __slots__ = ("schema_json", "version")
    SCHEMA_JSON_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    schema_json: str
    version: str
    def __init__(self, schema_json: _Optional[str] = ..., version: _Optional[str] = ...) -> None: ...

class ListRunsRequest(_message.Message):
    __slots__ = ("ctx", "project_id", "plan_id", "status", "page")
    CTX_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    PLAN_ID_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    project_id: str
    plan_id: str
    status: _types_pb2.RunStatus
    page: _types_pb2.PageRequest
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., project_id: _Optional[str] = ..., plan_id: _Optional[str] = ..., status: _Optional[_Union[_types_pb2.RunStatus, str]] = ..., page: _Optional[_Union[_types_pb2.PageRequest, _Mapping]] = ...) -> None: ...

class ListRunsResponse(_message.Message):
    __slots__ = ("runs", "page")
    RUNS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    runs: _containers.RepeatedCompositeFieldContainer[_types_pb2.TestRun]
    page: _types_pb2.PageResponse
    def __init__(self, runs: _Optional[_Iterable[_Union[_types_pb2.TestRun, _Mapping]]] = ..., page: _Optional[_Union[_types_pb2.PageResponse, _Mapping]] = ...) -> None: ...

class GetRunRequest(_message.Message):
    __slots__ = ("ctx", "run_id", "include_steps")
    CTX_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    INCLUDE_STEPS_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    run_id: str
    include_steps: bool
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., run_id: _Optional[str] = ..., include_steps: _Optional[bool] = ...) -> None: ...

class GetRunResponse(_message.Message):
    __slots__ = ("run", "case_results", "step_results")
    RUN_FIELD_NUMBER: _ClassVar[int]
    CASE_RESULTS_FIELD_NUMBER: _ClassVar[int]
    STEP_RESULTS_FIELD_NUMBER: _ClassVar[int]
    run: _types_pb2.TestRun
    case_results: _containers.RepeatedCompositeFieldContainer[_types_pb2.TestCaseResult]
    step_results: _containers.RepeatedCompositeFieldContainer[_types_pb2.TestStepResult]
    def __init__(self, run: _Optional[_Union[_types_pb2.TestRun, _Mapping]] = ..., case_results: _Optional[_Iterable[_Union[_types_pb2.TestCaseResult, _Mapping]]] = ..., step_results: _Optional[_Iterable[_Union[_types_pb2.TestStepResult, _Mapping]]] = ...) -> None: ...

class QueryCoverageRequest(_message.Message):
    __slots__ = ("ctx", "project_id")
    CTX_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    project_id: str
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., project_id: _Optional[str] = ...) -> None: ...

class QueryCoverageResponse(_message.Message):
    __slots__ = ("total_apis", "covered_apis", "uncovered_api_ids", "coverage_ratio")
    TOTAL_APIS_FIELD_NUMBER: _ClassVar[int]
    COVERED_APIS_FIELD_NUMBER: _ClassVar[int]
    UNCOVERED_API_IDS_FIELD_NUMBER: _ClassVar[int]
    COVERAGE_RATIO_FIELD_NUMBER: _ClassVar[int]
    total_apis: int
    covered_apis: int
    uncovered_api_ids: _containers.RepeatedScalarFieldContainer[str]
    coverage_ratio: float
    def __init__(self, total_apis: _Optional[int] = ..., covered_apis: _Optional[int] = ..., uncovered_api_ids: _Optional[_Iterable[str]] = ..., coverage_ratio: _Optional[float] = ...) -> None: ...

class ApiDirectoryEntry(_message.Message):
    __slots__ = ("node_id", "parent_id", "parent_name", "node_type", "name", "ref_id", "path", "method", "uri", "full_service", "rpc_method")
    NODE_ID_FIELD_NUMBER: _ClassVar[int]
    PARENT_ID_FIELD_NUMBER: _ClassVar[int]
    PARENT_NAME_FIELD_NUMBER: _ClassVar[int]
    NODE_TYPE_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    REF_ID_FIELD_NUMBER: _ClassVar[int]
    PATH_FIELD_NUMBER: _ClassVar[int]
    METHOD_FIELD_NUMBER: _ClassVar[int]
    URI_FIELD_NUMBER: _ClassVar[int]
    FULL_SERVICE_FIELD_NUMBER: _ClassVar[int]
    RPC_METHOD_FIELD_NUMBER: _ClassVar[int]
    node_id: str
    parent_id: str
    parent_name: str
    node_type: int
    name: str
    ref_id: str
    path: str
    method: int
    uri: str
    full_service: str
    rpc_method: str
    def __init__(self, node_id: _Optional[str] = ..., parent_id: _Optional[str] = ..., parent_name: _Optional[str] = ..., node_type: _Optional[int] = ..., name: _Optional[str] = ..., ref_id: _Optional[str] = ..., path: _Optional[str] = ..., method: _Optional[int] = ..., uri: _Optional[str] = ..., full_service: _Optional[str] = ..., rpc_method: _Optional[str] = ...) -> None: ...

class QueryApiDirectoryRequest(_message.Message):
    __slots__ = ("ctx", "project_id", "query", "parent_node_id")
    CTX_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    QUERY_FIELD_NUMBER: _ClassVar[int]
    PARENT_NODE_ID_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    project_id: str
    query: str
    parent_node_id: str
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., project_id: _Optional[str] = ..., query: _Optional[str] = ..., parent_node_id: _Optional[str] = ...) -> None: ...

class QueryApiDirectoryResponse(_message.Message):
    __slots__ = ("entries", "total", "summary")
    ENTRIES_FIELD_NUMBER: _ClassVar[int]
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    SUMMARY_FIELD_NUMBER: _ClassVar[int]
    entries: _containers.RepeatedCompositeFieldContainer[ApiDirectoryEntry]
    total: int
    summary: str
    def __init__(self, entries: _Optional[_Iterable[_Union[ApiDirectoryEntry, _Mapping]]] = ..., total: _Optional[int] = ..., summary: _Optional[str] = ...) -> None: ...

class VariableRefIssue(_message.Message):
    __slots__ = ("location", "field", "variable", "expression")
    LOCATION_FIELD_NUMBER: _ClassVar[int]
    FIELD_FIELD_NUMBER: _ClassVar[int]
    VARIABLE_FIELD_NUMBER: _ClassVar[int]
    EXPRESSION_FIELD_NUMBER: _ClassVar[int]
    location: str
    field: str
    variable: str
    expression: str
    def __init__(self, location: _Optional[str] = ..., field: _Optional[str] = ..., variable: _Optional[str] = ..., expression: _Optional[str] = ...) -> None: ...

class CheckVariableRefsRequest(_message.Message):
    __slots__ = ("ctx", "project_id", "environment_id")
    CTX_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    ENVIRONMENT_ID_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    project_id: str
    environment_id: str
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., project_id: _Optional[str] = ..., environment_id: _Optional[str] = ...) -> None: ...

class CheckVariableRefsResponse(_message.Message):
    __slots__ = ("defined_variables", "issues", "scanned_apis", "scanned_cases")
    DEFINED_VARIABLES_FIELD_NUMBER: _ClassVar[int]
    ISSUES_FIELD_NUMBER: _ClassVar[int]
    SCANNED_APIS_FIELD_NUMBER: _ClassVar[int]
    SCANNED_CASES_FIELD_NUMBER: _ClassVar[int]
    defined_variables: _containers.RepeatedScalarFieldContainer[str]
    issues: _containers.RepeatedCompositeFieldContainer[VariableRefIssue]
    scanned_apis: int
    scanned_cases: int
    def __init__(self, defined_variables: _Optional[_Iterable[str]] = ..., issues: _Optional[_Iterable[_Union[VariableRefIssue, _Mapping]]] = ..., scanned_apis: _Optional[int] = ..., scanned_cases: _Optional[int] = ...) -> None: ...

class CreateProjectRequest(_message.Message):
    __slots__ = ("ctx", "name", "description", "config")
    CTX_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    name: str
    description: str
    config: _struct_pb2.Struct
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., name: _Optional[str] = ..., description: _Optional[str] = ..., config: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ...) -> None: ...

class CreateProjectResponse(_message.Message):
    __slots__ = ("project_id",)
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    project_id: str
    def __init__(self, project_id: _Optional[str] = ...) -> None: ...

class CreateApiRequest(_message.Message):
    __slots__ = ("ctx", "project_id", "http", "grpc", "parent_node_id")
    CTX_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    HTTP_FIELD_NUMBER: _ClassVar[int]
    GRPC_FIELD_NUMBER: _ClassVar[int]
    PARENT_NODE_ID_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    project_id: str
    http: _types_pb2.HttpApi
    grpc: _types_pb2.GrpcApi
    parent_node_id: str
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., project_id: _Optional[str] = ..., http: _Optional[_Union[_types_pb2.HttpApi, _Mapping]] = ..., grpc: _Optional[_Union[_types_pb2.GrpcApi, _Mapping]] = ..., parent_node_id: _Optional[str] = ...) -> None: ...

class CreateApiResponse(_message.Message):
    __slots__ = ("api_id", "node_id")
    API_ID_FIELD_NUMBER: _ClassVar[int]
    NODE_ID_FIELD_NUMBER: _ClassVar[int]
    api_id: str
    node_id: str
    def __init__(self, api_id: _Optional[str] = ..., node_id: _Optional[str] = ...) -> None: ...

class UpdateApiRequest(_message.Message):
    __slots__ = ("ctx", "api_id", "kind", "http", "grpc")
    CTX_FIELD_NUMBER: _ClassVar[int]
    API_ID_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    HTTP_FIELD_NUMBER: _ClassVar[int]
    GRPC_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    api_id: str
    kind: ApiKind
    http: _types_pb2.HttpApi
    grpc: _types_pb2.GrpcApi
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., api_id: _Optional[str] = ..., kind: _Optional[_Union[ApiKind, str]] = ..., http: _Optional[_Union[_types_pb2.HttpApi, _Mapping]] = ..., grpc: _Optional[_Union[_types_pb2.GrpcApi, _Mapping]] = ...) -> None: ...

class UpdateApiResponse(_message.Message):
    __slots__ = ("api_id",)
    API_ID_FIELD_NUMBER: _ClassVar[int]
    api_id: str
    def __init__(self, api_id: _Optional[str] = ...) -> None: ...

class CreateTestCaseRequest(_message.Message):
    __slots__ = ("ctx", "project_id", "case", "parent_node_id")
    CTX_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    CASE_FIELD_NUMBER: _ClassVar[int]
    PARENT_NODE_ID_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    project_id: str
    case: _types_pb2.TestCase
    parent_node_id: str
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., project_id: _Optional[str] = ..., case: _Optional[_Union[_types_pb2.TestCase, _Mapping]] = ..., parent_node_id: _Optional[str] = ...) -> None: ...

class CreateTestCaseResponse(_message.Message):
    __slots__ = ("case_id", "node_id")
    CASE_ID_FIELD_NUMBER: _ClassVar[int]
    NODE_ID_FIELD_NUMBER: _ClassVar[int]
    case_id: str
    node_id: str
    def __init__(self, case_id: _Optional[str] = ..., node_id: _Optional[str] = ...) -> None: ...

class UpdateTestCaseRequest(_message.Message):
    __slots__ = ("ctx", "case_id", "case")
    CTX_FIELD_NUMBER: _ClassVar[int]
    CASE_ID_FIELD_NUMBER: _ClassVar[int]
    CASE_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    case_id: str
    case: _types_pb2.TestCase
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., case_id: _Optional[str] = ..., case: _Optional[_Union[_types_pb2.TestCase, _Mapping]] = ...) -> None: ...

class UpdateTestCaseResponse(_message.Message):
    __slots__ = ("case_id",)
    CASE_ID_FIELD_NUMBER: _ClassVar[int]
    case_id: str
    def __init__(self, case_id: _Optional[str] = ...) -> None: ...

class CreateTestPlanRequest(_message.Message):
    __slots__ = ("ctx", "project_id", "plan", "parent_node_id")
    CTX_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    PLAN_FIELD_NUMBER: _ClassVar[int]
    PARENT_NODE_ID_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    project_id: str
    plan: _types_pb2.TestPlan
    parent_node_id: str
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., project_id: _Optional[str] = ..., plan: _Optional[_Union[_types_pb2.TestPlan, _Mapping]] = ..., parent_node_id: _Optional[str] = ...) -> None: ...

class CreateTestPlanResponse(_message.Message):
    __slots__ = ("plan_id", "node_id")
    PLAN_ID_FIELD_NUMBER: _ClassVar[int]
    NODE_ID_FIELD_NUMBER: _ClassVar[int]
    plan_id: str
    node_id: str
    def __init__(self, plan_id: _Optional[str] = ..., node_id: _Optional[str] = ...) -> None: ...

class ImportOpenApiRequest(_message.Message):
    __slots__ = ("ctx", "project_id", "openapi_url", "openapi_document", "parent_node_id", "generate_cases")
    CTX_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    OPENAPI_URL_FIELD_NUMBER: _ClassVar[int]
    OPENAPI_DOCUMENT_FIELD_NUMBER: _ClassVar[int]
    PARENT_NODE_ID_FIELD_NUMBER: _ClassVar[int]
    GENERATE_CASES_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    project_id: str
    openapi_url: str
    openapi_document: str
    parent_node_id: str
    generate_cases: bool
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., project_id: _Optional[str] = ..., openapi_url: _Optional[str] = ..., openapi_document: _Optional[str] = ..., parent_node_id: _Optional[str] = ..., generate_cases: _Optional[bool] = ...) -> None: ...

class ImportOpenApiResponse(_message.Message):
    __slots__ = ("api_ids", "imported_count", "case_ids")
    API_IDS_FIELD_NUMBER: _ClassVar[int]
    IMPORTED_COUNT_FIELD_NUMBER: _ClassVar[int]
    CASE_IDS_FIELD_NUMBER: _ClassVar[int]
    api_ids: _containers.RepeatedScalarFieldContainer[str]
    imported_count: int
    case_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, api_ids: _Optional[_Iterable[str]] = ..., imported_count: _Optional[int] = ..., case_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class DiffEntry(_message.Message):
    __slots__ = ("api_id", "kind", "summary")
    API_ID_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    SUMMARY_FIELD_NUMBER: _ClassVar[int]
    api_id: str
    kind: str
    summary: str
    def __init__(self, api_id: _Optional[str] = ..., kind: _Optional[str] = ..., summary: _Optional[str] = ...) -> None: ...

class ApplyOpenApiDiffRequest(_message.Message):
    __slots__ = ("ctx", "project_id", "openapi_document", "auto_update_cases")
    CTX_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    OPENAPI_DOCUMENT_FIELD_NUMBER: _ClassVar[int]
    AUTO_UPDATE_CASES_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    project_id: str
    openapi_document: str
    auto_update_cases: bool
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., project_id: _Optional[str] = ..., openapi_document: _Optional[str] = ..., auto_update_cases: _Optional[bool] = ...) -> None: ...

class ApplyOpenApiDiffResponse(_message.Message):
    __slots__ = ("diffs", "updated_api_ids", "updated_case_ids")
    DIFFS_FIELD_NUMBER: _ClassVar[int]
    UPDATED_API_IDS_FIELD_NUMBER: _ClassVar[int]
    UPDATED_CASE_IDS_FIELD_NUMBER: _ClassVar[int]
    diffs: _containers.RepeatedCompositeFieldContainer[DiffEntry]
    updated_api_ids: _containers.RepeatedScalarFieldContainer[str]
    updated_case_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, diffs: _Optional[_Iterable[_Union[DiffEntry, _Mapping]]] = ..., updated_api_ids: _Optional[_Iterable[str]] = ..., updated_case_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class TriggerRunRequest(_message.Message):
    __slots__ = ("ctx", "plan_id", "env_id")
    CTX_FIELD_NUMBER: _ClassVar[int]
    PLAN_ID_FIELD_NUMBER: _ClassVar[int]
    ENV_ID_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    plan_id: str
    env_id: str
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., plan_id: _Optional[str] = ..., env_id: _Optional[str] = ...) -> None: ...

class TriggerRunResponse(_message.Message):
    __slots__ = ("run_id",)
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    run_id: str
    def __init__(self, run_id: _Optional[str] = ...) -> None: ...

class TriggerStressRequest(_message.Message):
    __slots__ = ("ctx", "stress_plan_id", "env_id")
    CTX_FIELD_NUMBER: _ClassVar[int]
    STRESS_PLAN_ID_FIELD_NUMBER: _ClassVar[int]
    ENV_ID_FIELD_NUMBER: _ClassVar[int]
    ctx: _types_pb2.RequestContext
    stress_plan_id: str
    env_id: str
    def __init__(self, ctx: _Optional[_Union[_types_pb2.RequestContext, _Mapping]] = ..., stress_plan_id: _Optional[str] = ..., env_id: _Optional[str] = ...) -> None: ...

class TriggerStressResponse(_message.Message):
    __slots__ = ("run_id",)
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    run_id: str
    def __init__(self, run_id: _Optional[str] = ...) -> None: ...
