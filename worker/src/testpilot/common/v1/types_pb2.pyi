import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf import duration_pb2 as _duration_pb2
from google.protobuf import struct_pb2 as _struct_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class VariableScope(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    VARIABLE_SCOPE_UNSPECIFIED: _ClassVar[VariableScope]
    VARIABLE_SCOPE_PROJECT: _ClassVar[VariableScope]
    VARIABLE_SCOPE_ENVIRONMENT: _ClassVar[VariableScope]

class VariableCategory(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    VARIABLE_CATEGORY_UNSPECIFIED: _ClassVar[VariableCategory]
    VARIABLE_CATEGORY_HEADER: _ClassVar[VariableCategory]
    VARIABLE_CATEGORY_COOKIE: _ClassVar[VariableCategory]
    VARIABLE_CATEGORY_QUERY: _ClassVar[VariableCategory]
    VARIABLE_CATEGORY_BODY: _ClassVar[VariableCategory]
    VARIABLE_CATEGORY_CUSTOM: _ClassVar[VariableCategory]

class HttpMethod(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    HTTP_METHOD_UNSPECIFIED: _ClassVar[HttpMethod]
    HTTP_METHOD_GET: _ClassVar[HttpMethod]
    HTTP_METHOD_POST: _ClassVar[HttpMethod]
    HTTP_METHOD_PUT: _ClassVar[HttpMethod]
    HTTP_METHOD_DELETE: _ClassVar[HttpMethod]
    HTTP_METHOD_PATCH: _ClassVar[HttpMethod]
    HTTP_METHOD_HEAD: _ClassVar[HttpMethod]
    HTTP_METHOD_OPTIONS: _ClassVar[HttpMethod]

class BodyContentType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    BODY_CONTENT_TYPE_UNSPECIFIED: _ClassVar[BodyContentType]
    BODY_CONTENT_TYPE_NONE: _ClassVar[BodyContentType]
    BODY_CONTENT_TYPE_FORM_DATA: _ClassVar[BodyContentType]
    BODY_CONTENT_TYPE_X_WWW_FORM_URLENCODED: _ClassVar[BodyContentType]
    BODY_CONTENT_TYPE_JSON: _ClassVar[BodyContentType]
    BODY_CONTENT_TYPE_XML: _ClassVar[BodyContentType]
    BODY_CONTENT_TYPE_BINARY: _ClassVar[BodyContentType]
    BODY_CONTENT_TYPE_GRAPHQL: _ClassVar[BodyContentType]

class NodeType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    NODE_TYPE_UNSPECIFIED: _ClassVar[NodeType]
    NODE_TYPE_FOLDER: _ClassVar[NodeType]
    NODE_TYPE_HTTP_API: _ClassVar[NodeType]
    NODE_TYPE_GRPC_API: _ClassVar[NodeType]
    NODE_TYPE_TEST_CASE: _ClassVar[NodeType]
    NODE_TYPE_TEST_SUITE: _ClassVar[NodeType]
    NODE_TYPE_TEST_PLAN: _ClassVar[NodeType]

class TestCaseType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TEST_CASE_TYPE_UNSPECIFIED: _ClassVar[TestCaseType]
    TEST_CASE_TYPE_DECLARATIVE: _ClassVar[TestCaseType]
    TEST_CASE_TYPE_LOWCODE: _ClassVar[TestCaseType]

class TriggerType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TRIGGER_TYPE_UNSPECIFIED: _ClassVar[TriggerType]
    TRIGGER_TYPE_MANUAL: _ClassVar[TriggerType]
    TRIGGER_TYPE_SCHEDULED: _ClassVar[TriggerType]
    TRIGGER_TYPE_CI: _ClassVar[TriggerType]

class OverlapPolicy(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    OVERLAP_POLICY_UNSPECIFIED: _ClassVar[OverlapPolicy]
    OVERLAP_POLICY_SKIP: _ClassVar[OverlapPolicy]
    OVERLAP_POLICY_QUEUE: _ClassVar[OverlapPolicy]
    OVERLAP_POLICY_RUN: _ClassVar[OverlapPolicy]

class RunStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    RUN_STATUS_UNSPECIFIED: _ClassVar[RunStatus]
    RUN_STATUS_RUNNING: _ClassVar[RunStatus]
    RUN_STATUS_PASSED: _ClassVar[RunStatus]
    RUN_STATUS_FAILED: _ClassVar[RunStatus]
    RUN_STATUS_ABORTED: _ClassVar[RunStatus]
    RUN_STATUS_TIMEOUT: _ClassVar[RunStatus]

class CaseStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CASE_STATUS_UNSPECIFIED: _ClassVar[CaseStatus]
    CASE_STATUS_RUNNING: _ClassVar[CaseStatus]
    CASE_STATUS_PASSED: _ClassVar[CaseStatus]
    CASE_STATUS_FAILED: _ClassVar[CaseStatus]
    CASE_STATUS_SKIPPED: _ClassVar[CaseStatus]

class StepStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    STEP_STATUS_UNSPECIFIED: _ClassVar[StepStatus]
    STEP_STATUS_RUNNING: _ClassVar[StepStatus]
    STEP_STATUS_PASSED: _ClassVar[StepStatus]
    STEP_STATUS_FAILED: _ClassVar[StepStatus]
    STEP_STATUS_SKIPPED: _ClassVar[StepStatus]

class Capability(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CAPABILITY_UNSPECIFIED: _ClassVar[Capability]
    CAPABILITY_FUNCTIONAL: _ClassVar[Capability]
    CAPABILITY_LOWCODE: _ClassVar[Capability]
    CAPABILITY_PLAYWRIGHT: _ClassVar[Capability]
    CAPABILITY_STRESS: _ClassVar[Capability]

class TaskType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TASK_TYPE_UNSPECIFIED: _ClassVar[TaskType]
    TASK_TYPE_FUNCTIONAL_DECLARATIVE: _ClassVar[TaskType]
    TASK_TYPE_FUNCTIONAL_LOWCODE: _ClassVar[TaskType]
    TASK_TYPE_PLAYWRIGHT: _ClassVar[TaskType]
    TASK_TYPE_STRESS: _ClassVar[TaskType]

class AssertionTarget(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ASSERTION_TARGET_UNSPECIFIED: _ClassVar[AssertionTarget]
    ASSERTION_TARGET_STATUS: _ClassVar[AssertionTarget]
    ASSERTION_TARGET_HEADER: _ClassVar[AssertionTarget]
    ASSERTION_TARGET_BODY: _ClassVar[AssertionTarget]
    ASSERTION_TARGET_JSONPATH: _ClassVar[AssertionTarget]
    ASSERTION_TARGET_ELAPSED: _ClassVar[AssertionTarget]
    ASSERTION_TARGET_CUSTOM: _ClassVar[AssertionTarget]

class AssertionOp(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ASSERTION_OP_UNSPECIFIED: _ClassVar[AssertionOp]
    ASSERTION_OP_EQ: _ClassVar[AssertionOp]
    ASSERTION_OP_NE: _ClassVar[AssertionOp]
    ASSERTION_OP_EXISTS: _ClassVar[AssertionOp]
    ASSERTION_OP_NOT_EXISTS: _ClassVar[AssertionOp]
    ASSERTION_OP_CONTAINS: _ClassVar[AssertionOp]
    ASSERTION_OP_MATCHES: _ClassVar[AssertionOp]
    ASSERTION_OP_GT: _ClassVar[AssertionOp]
    ASSERTION_OP_LT: _ClassVar[AssertionOp]
    ASSERTION_OP_GE: _ClassVar[AssertionOp]
    ASSERTION_OP_LE: _ClassVar[AssertionOp]
    ASSERTION_OP_TYPE_IS: _ClassVar[AssertionOp]

class StepType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    STEP_TYPE_UNSPECIFIED: _ClassVar[StepType]
    STEP_TYPE_API_CALL: _ClassVar[StepType]
    STEP_TYPE_GRPC_CALL: _ClassVar[StepType]
    STEP_TYPE_ASSERTION: _ClassVar[StepType]
    STEP_TYPE_SET_VAR: _ClassVar[StepType]
    STEP_TYPE_IF: _ClassVar[StepType]
    STEP_TYPE_LOOP: _ClassVar[StepType]
    STEP_TYPE_RETRY: _ClassVar[StepType]
    STEP_TYPE_CODE_BLOCK: _ClassVar[StepType]
    STEP_TYPE_DELAY: _ClassVar[StepType]
    STEP_TYPE_UI_ACTION: _ClassVar[StepType]

class UiAction(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    UI_ACTION_UNSPECIFIED: _ClassVar[UiAction]
    UI_ACTION_GOTO: _ClassVar[UiAction]
    UI_ACTION_CLICK: _ClassVar[UiAction]
    UI_ACTION_FILL: _ClassVar[UiAction]
    UI_ACTION_SELECT: _ClassVar[UiAction]
    UI_ACTION_CHECK: _ClassVar[UiAction]
    UI_ACTION_HOVER: _ClassVar[UiAction]
    UI_ACTION_PRESS: _ClassVar[UiAction]
    UI_ACTION_EXPECT_TEXT: _ClassVar[UiAction]
    UI_ACTION_EXPECT_VISIBLE: _ClassVar[UiAction]
    UI_ACTION_SCREENSHOT: _ClassVar[UiAction]
    UI_ACTION_WAIT: _ClassVar[UiAction]
    UI_ACTION_UPLOAD: _ClassVar[UiAction]
    UI_ACTION_DOWNLOAD: _ClassVar[UiAction]
VARIABLE_SCOPE_UNSPECIFIED: VariableScope
VARIABLE_SCOPE_PROJECT: VariableScope
VARIABLE_SCOPE_ENVIRONMENT: VariableScope
VARIABLE_CATEGORY_UNSPECIFIED: VariableCategory
VARIABLE_CATEGORY_HEADER: VariableCategory
VARIABLE_CATEGORY_COOKIE: VariableCategory
VARIABLE_CATEGORY_QUERY: VariableCategory
VARIABLE_CATEGORY_BODY: VariableCategory
VARIABLE_CATEGORY_CUSTOM: VariableCategory
HTTP_METHOD_UNSPECIFIED: HttpMethod
HTTP_METHOD_GET: HttpMethod
HTTP_METHOD_POST: HttpMethod
HTTP_METHOD_PUT: HttpMethod
HTTP_METHOD_DELETE: HttpMethod
HTTP_METHOD_PATCH: HttpMethod
HTTP_METHOD_HEAD: HttpMethod
HTTP_METHOD_OPTIONS: HttpMethod
BODY_CONTENT_TYPE_UNSPECIFIED: BodyContentType
BODY_CONTENT_TYPE_NONE: BodyContentType
BODY_CONTENT_TYPE_FORM_DATA: BodyContentType
BODY_CONTENT_TYPE_X_WWW_FORM_URLENCODED: BodyContentType
BODY_CONTENT_TYPE_JSON: BodyContentType
BODY_CONTENT_TYPE_XML: BodyContentType
BODY_CONTENT_TYPE_BINARY: BodyContentType
BODY_CONTENT_TYPE_GRAPHQL: BodyContentType
NODE_TYPE_UNSPECIFIED: NodeType
NODE_TYPE_FOLDER: NodeType
NODE_TYPE_HTTP_API: NodeType
NODE_TYPE_GRPC_API: NodeType
NODE_TYPE_TEST_CASE: NodeType
NODE_TYPE_TEST_SUITE: NodeType
NODE_TYPE_TEST_PLAN: NodeType
TEST_CASE_TYPE_UNSPECIFIED: TestCaseType
TEST_CASE_TYPE_DECLARATIVE: TestCaseType
TEST_CASE_TYPE_LOWCODE: TestCaseType
TRIGGER_TYPE_UNSPECIFIED: TriggerType
TRIGGER_TYPE_MANUAL: TriggerType
TRIGGER_TYPE_SCHEDULED: TriggerType
TRIGGER_TYPE_CI: TriggerType
OVERLAP_POLICY_UNSPECIFIED: OverlapPolicy
OVERLAP_POLICY_SKIP: OverlapPolicy
OVERLAP_POLICY_QUEUE: OverlapPolicy
OVERLAP_POLICY_RUN: OverlapPolicy
RUN_STATUS_UNSPECIFIED: RunStatus
RUN_STATUS_RUNNING: RunStatus
RUN_STATUS_PASSED: RunStatus
RUN_STATUS_FAILED: RunStatus
RUN_STATUS_ABORTED: RunStatus
RUN_STATUS_TIMEOUT: RunStatus
CASE_STATUS_UNSPECIFIED: CaseStatus
CASE_STATUS_RUNNING: CaseStatus
CASE_STATUS_PASSED: CaseStatus
CASE_STATUS_FAILED: CaseStatus
CASE_STATUS_SKIPPED: CaseStatus
STEP_STATUS_UNSPECIFIED: StepStatus
STEP_STATUS_RUNNING: StepStatus
STEP_STATUS_PASSED: StepStatus
STEP_STATUS_FAILED: StepStatus
STEP_STATUS_SKIPPED: StepStatus
CAPABILITY_UNSPECIFIED: Capability
CAPABILITY_FUNCTIONAL: Capability
CAPABILITY_LOWCODE: Capability
CAPABILITY_PLAYWRIGHT: Capability
CAPABILITY_STRESS: Capability
TASK_TYPE_UNSPECIFIED: TaskType
TASK_TYPE_FUNCTIONAL_DECLARATIVE: TaskType
TASK_TYPE_FUNCTIONAL_LOWCODE: TaskType
TASK_TYPE_PLAYWRIGHT: TaskType
TASK_TYPE_STRESS: TaskType
ASSERTION_TARGET_UNSPECIFIED: AssertionTarget
ASSERTION_TARGET_STATUS: AssertionTarget
ASSERTION_TARGET_HEADER: AssertionTarget
ASSERTION_TARGET_BODY: AssertionTarget
ASSERTION_TARGET_JSONPATH: AssertionTarget
ASSERTION_TARGET_ELAPSED: AssertionTarget
ASSERTION_TARGET_CUSTOM: AssertionTarget
ASSERTION_OP_UNSPECIFIED: AssertionOp
ASSERTION_OP_EQ: AssertionOp
ASSERTION_OP_NE: AssertionOp
ASSERTION_OP_EXISTS: AssertionOp
ASSERTION_OP_NOT_EXISTS: AssertionOp
ASSERTION_OP_CONTAINS: AssertionOp
ASSERTION_OP_MATCHES: AssertionOp
ASSERTION_OP_GT: AssertionOp
ASSERTION_OP_LT: AssertionOp
ASSERTION_OP_GE: AssertionOp
ASSERTION_OP_LE: AssertionOp
ASSERTION_OP_TYPE_IS: AssertionOp
STEP_TYPE_UNSPECIFIED: StepType
STEP_TYPE_API_CALL: StepType
STEP_TYPE_GRPC_CALL: StepType
STEP_TYPE_ASSERTION: StepType
STEP_TYPE_SET_VAR: StepType
STEP_TYPE_IF: StepType
STEP_TYPE_LOOP: StepType
STEP_TYPE_RETRY: StepType
STEP_TYPE_CODE_BLOCK: StepType
STEP_TYPE_DELAY: StepType
STEP_TYPE_UI_ACTION: StepType
UI_ACTION_UNSPECIFIED: UiAction
UI_ACTION_GOTO: UiAction
UI_ACTION_CLICK: UiAction
UI_ACTION_FILL: UiAction
UI_ACTION_SELECT: UiAction
UI_ACTION_CHECK: UiAction
UI_ACTION_HOVER: UiAction
UI_ACTION_PRESS: UiAction
UI_ACTION_EXPECT_TEXT: UiAction
UI_ACTION_EXPECT_VISIBLE: UiAction
UI_ACTION_SCREENSHOT: UiAction
UI_ACTION_WAIT: UiAction
UI_ACTION_UPLOAD: UiAction
UI_ACTION_DOWNLOAD: UiAction

class RequestContext(_message.Message):
    __slots__ = ("tenant_id", "user_id", "actor", "request_id")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    ACTOR_FIELD_NUMBER: _ClassVar[int]
    REQUEST_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: int
    user_id: str
    actor: str
    request_id: str
    def __init__(self, tenant_id: _Optional[int] = ..., user_id: _Optional[str] = ..., actor: _Optional[str] = ..., request_id: _Optional[str] = ...) -> None: ...

class PageRequest(_message.Message):
    __slots__ = ("page", "page_size")
    PAGE_FIELD_NUMBER: _ClassVar[int]
    PAGE_SIZE_FIELD_NUMBER: _ClassVar[int]
    page: int
    page_size: int
    def __init__(self, page: _Optional[int] = ..., page_size: _Optional[int] = ...) -> None: ...

class PageResponse(_message.Message):
    __slots__ = ("total", "page", "page_size")
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    PAGE_SIZE_FIELD_NUMBER: _ClassVar[int]
    total: int
    page: int
    page_size: int
    def __init__(self, total: _Optional[int] = ..., page: _Optional[int] = ..., page_size: _Optional[int] = ...) -> None: ...

class KeyValue(_message.Message):
    __slots__ = ("key", "value", "description", "enabled")
    KEY_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    key: str
    value: str
    description: str
    enabled: bool
    def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ..., description: _Optional[str] = ..., enabled: _Optional[bool] = ...) -> None: ...

class CookieParam(_message.Message):
    __slots__ = ("name", "value", "type", "description")
    NAME_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    name: str
    value: str
    type: str
    description: str
    def __init__(self, name: _Optional[str] = ..., value: _Optional[str] = ..., type: _Optional[str] = ..., description: _Optional[str] = ...) -> None: ...

class FormField(_message.Message):
    __slots__ = ("key", "value", "type", "description")
    KEY_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    key: str
    value: str
    type: str
    description: str
    def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ..., type: _Optional[str] = ..., description: _Optional[str] = ...) -> None: ...

class FormData(_message.Message):
    __slots__ = ("fields",)
    FIELDS_FIELD_NUMBER: _ClassVar[int]
    fields: _containers.RepeatedCompositeFieldContainer[FormField]
    def __init__(self, fields: _Optional[_Iterable[_Union[FormField, _Mapping]]] = ...) -> None: ...

class BodySpec(_message.Message):
    __slots__ = ("content_type", "raw", "form", "binary_ref")
    CONTENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    RAW_FIELD_NUMBER: _ClassVar[int]
    FORM_FIELD_NUMBER: _ClassVar[int]
    BINARY_REF_FIELD_NUMBER: _ClassVar[int]
    content_type: BodyContentType
    raw: str
    form: FormData
    binary_ref: str
    def __init__(self, content_type: _Optional[_Union[BodyContentType, str]] = ..., raw: _Optional[str] = ..., form: _Optional[_Union[FormData, _Mapping]] = ..., binary_ref: _Optional[str] = ...) -> None: ...

class ApiSettings(_message.Message):
    __slots__ = ("tls_verify", "follow_redirects", "comment_tolerant_json", "timeout")
    TLS_VERIFY_FIELD_NUMBER: _ClassVar[int]
    FOLLOW_REDIRECTS_FIELD_NUMBER: _ClassVar[int]
    COMMENT_TOLERANT_JSON_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    tls_verify: bool
    follow_redirects: bool
    comment_tolerant_json: bool
    timeout: _duration_pb2.Duration
    def __init__(self, tls_verify: _Optional[bool] = ..., follow_redirects: _Optional[bool] = ..., comment_tolerant_json: _Optional[bool] = ..., timeout: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ...) -> None: ...

class TlsSettings(_message.Message):
    __slots__ = ("enabled", "skip_verify", "certificate_id")
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    SKIP_VERIFY_FIELD_NUMBER: _ClassVar[int]
    CERTIFICATE_ID_FIELD_NUMBER: _ClassVar[int]
    enabled: bool
    skip_verify: bool
    certificate_id: str
    def __init__(self, enabled: _Optional[bool] = ..., skip_verify: _Optional[bool] = ..., certificate_id: _Optional[str] = ...) -> None: ...

class Script(_message.Message):
    __slots__ = ("id", "lang", "source", "enabled")
    ID_FIELD_NUMBER: _ClassVar[int]
    LANG_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    id: str
    lang: str
    source: str
    enabled: bool
    def __init__(self, id: _Optional[str] = ..., lang: _Optional[str] = ..., source: _Optional[str] = ..., enabled: _Optional[bool] = ...) -> None: ...

class Project(_message.Message):
    __slots__ = ("id", "tenant_id", "name", "description", "config", "created_at", "updated_at")
    ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    id: str
    tenant_id: int
    name: str
    description: str
    config: _struct_pb2.Struct
    created_at: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    def __init__(self, id: _Optional[str] = ..., tenant_id: _Optional[int] = ..., name: _Optional[str] = ..., description: _Optional[str] = ..., config: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class Variable(_message.Message):
    __slots__ = ("id", "tenant_id", "project_id", "environment_id", "scope", "category", "key", "value", "sensitive", "secret_ref", "description")
    ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    ENVIRONMENT_ID_FIELD_NUMBER: _ClassVar[int]
    SCOPE_FIELD_NUMBER: _ClassVar[int]
    CATEGORY_FIELD_NUMBER: _ClassVar[int]
    KEY_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    SENSITIVE_FIELD_NUMBER: _ClassVar[int]
    SECRET_REF_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    id: str
    tenant_id: int
    project_id: str
    environment_id: str
    scope: VariableScope
    category: VariableCategory
    key: str
    value: str
    sensitive: bool
    secret_ref: str
    description: str
    def __init__(self, id: _Optional[str] = ..., tenant_id: _Optional[int] = ..., project_id: _Optional[str] = ..., environment_id: _Optional[str] = ..., scope: _Optional[_Union[VariableScope, str]] = ..., category: _Optional[_Union[VariableCategory, str]] = ..., key: _Optional[str] = ..., value: _Optional[str] = ..., sensitive: _Optional[bool] = ..., secret_ref: _Optional[str] = ..., description: _Optional[str] = ...) -> None: ...

class Environment(_message.Message):
    __slots__ = ("id", "tenant_id", "project_id", "icon", "name", "description", "base_url", "variables")
    ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    ICON_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    BASE_URL_FIELD_NUMBER: _ClassVar[int]
    VARIABLES_FIELD_NUMBER: _ClassVar[int]
    id: str
    tenant_id: int
    project_id: str
    icon: str
    name: str
    description: str
    base_url: str
    variables: _containers.RepeatedCompositeFieldContainer[Variable]
    def __init__(self, id: _Optional[str] = ..., tenant_id: _Optional[int] = ..., project_id: _Optional[str] = ..., icon: _Optional[str] = ..., name: _Optional[str] = ..., description: _Optional[str] = ..., base_url: _Optional[str] = ..., variables: _Optional[_Iterable[_Union[Variable, _Mapping]]] = ...) -> None: ...

class Certificate(_message.Message):
    __slots__ = ("id", "tenant_id", "project_id", "name", "description", "type", "cert_ref", "key_ref", "password_secret_ref")
    ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    CERT_REF_FIELD_NUMBER: _ClassVar[int]
    KEY_REF_FIELD_NUMBER: _ClassVar[int]
    PASSWORD_SECRET_REF_FIELD_NUMBER: _ClassVar[int]
    id: str
    tenant_id: int
    project_id: str
    name: str
    description: str
    type: str
    cert_ref: str
    key_ref: str
    password_secret_ref: str
    def __init__(self, id: _Optional[str] = ..., tenant_id: _Optional[int] = ..., project_id: _Optional[str] = ..., name: _Optional[str] = ..., description: _Optional[str] = ..., type: _Optional[str] = ..., cert_ref: _Optional[str] = ..., key_ref: _Optional[str] = ..., password_secret_ref: _Optional[str] = ...) -> None: ...

class TreeNode(_message.Message):
    __slots__ = ("id", "tenant_id", "project_id", "parent_id", "node_type", "ref_id", "name", "icon", "order", "path")
    ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    PARENT_ID_FIELD_NUMBER: _ClassVar[int]
    NODE_TYPE_FIELD_NUMBER: _ClassVar[int]
    REF_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    ICON_FIELD_NUMBER: _ClassVar[int]
    ORDER_FIELD_NUMBER: _ClassVar[int]
    PATH_FIELD_NUMBER: _ClassVar[int]
    id: str
    tenant_id: int
    project_id: str
    parent_id: str
    node_type: NodeType
    ref_id: str
    name: str
    icon: str
    order: int
    path: str
    def __init__(self, id: _Optional[str] = ..., tenant_id: _Optional[int] = ..., project_id: _Optional[str] = ..., parent_id: _Optional[str] = ..., node_type: _Optional[_Union[NodeType, str]] = ..., ref_id: _Optional[str] = ..., name: _Optional[str] = ..., icon: _Optional[str] = ..., order: _Optional[int] = ..., path: _Optional[str] = ...) -> None: ...

class HttpApi(_message.Message):
    __slots__ = ("id", "tenant_id", "project_id", "method", "uri", "params", "body", "headers", "cookies", "pre_scripts", "post_scripts", "settings", "certificate_id")
    ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    METHOD_FIELD_NUMBER: _ClassVar[int]
    URI_FIELD_NUMBER: _ClassVar[int]
    PARAMS_FIELD_NUMBER: _ClassVar[int]
    BODY_FIELD_NUMBER: _ClassVar[int]
    HEADERS_FIELD_NUMBER: _ClassVar[int]
    COOKIES_FIELD_NUMBER: _ClassVar[int]
    PRE_SCRIPTS_FIELD_NUMBER: _ClassVar[int]
    POST_SCRIPTS_FIELD_NUMBER: _ClassVar[int]
    SETTINGS_FIELD_NUMBER: _ClassVar[int]
    CERTIFICATE_ID_FIELD_NUMBER: _ClassVar[int]
    id: str
    tenant_id: int
    project_id: str
    method: HttpMethod
    uri: str
    params: _containers.RepeatedCompositeFieldContainer[KeyValue]
    body: BodySpec
    headers: _containers.RepeatedCompositeFieldContainer[KeyValue]
    cookies: _containers.RepeatedCompositeFieldContainer[CookieParam]
    pre_scripts: _containers.RepeatedCompositeFieldContainer[Script]
    post_scripts: _containers.RepeatedCompositeFieldContainer[Script]
    settings: ApiSettings
    certificate_id: str
    def __init__(self, id: _Optional[str] = ..., tenant_id: _Optional[int] = ..., project_id: _Optional[str] = ..., method: _Optional[_Union[HttpMethod, str]] = ..., uri: _Optional[str] = ..., params: _Optional[_Iterable[_Union[KeyValue, _Mapping]]] = ..., body: _Optional[_Union[BodySpec, _Mapping]] = ..., headers: _Optional[_Iterable[_Union[KeyValue, _Mapping]]] = ..., cookies: _Optional[_Iterable[_Union[CookieParam, _Mapping]]] = ..., pre_scripts: _Optional[_Iterable[_Union[Script, _Mapping]]] = ..., post_scripts: _Optional[_Iterable[_Union[Script, _Mapping]]] = ..., settings: _Optional[_Union[ApiSettings, _Mapping]] = ..., certificate_id: _Optional[str] = ...) -> None: ...

class GrpcApi(_message.Message):
    __slots__ = ("id", "tenant_id", "project_id", "proto_ref", "full_service", "method", "request_message", "metadata", "deadline", "tls_settings", "pre_scripts", "post_scripts", "certificate_id")
    ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    PROTO_REF_FIELD_NUMBER: _ClassVar[int]
    FULL_SERVICE_FIELD_NUMBER: _ClassVar[int]
    METHOD_FIELD_NUMBER: _ClassVar[int]
    REQUEST_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    METADATA_FIELD_NUMBER: _ClassVar[int]
    DEADLINE_FIELD_NUMBER: _ClassVar[int]
    TLS_SETTINGS_FIELD_NUMBER: _ClassVar[int]
    PRE_SCRIPTS_FIELD_NUMBER: _ClassVar[int]
    POST_SCRIPTS_FIELD_NUMBER: _ClassVar[int]
    CERTIFICATE_ID_FIELD_NUMBER: _ClassVar[int]
    id: str
    tenant_id: int
    project_id: str
    proto_ref: str
    full_service: str
    method: str
    request_message: _struct_pb2.Struct
    metadata: _containers.RepeatedCompositeFieldContainer[KeyValue]
    deadline: _duration_pb2.Duration
    tls_settings: TlsSettings
    pre_scripts: _containers.RepeatedCompositeFieldContainer[Script]
    post_scripts: _containers.RepeatedCompositeFieldContainer[Script]
    certificate_id: str
    def __init__(self, id: _Optional[str] = ..., tenant_id: _Optional[int] = ..., project_id: _Optional[str] = ..., proto_ref: _Optional[str] = ..., full_service: _Optional[str] = ..., method: _Optional[str] = ..., request_message: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., metadata: _Optional[_Iterable[_Union[KeyValue, _Mapping]]] = ..., deadline: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., tls_settings: _Optional[_Union[TlsSettings, _Mapping]] = ..., pre_scripts: _Optional[_Iterable[_Union[Script, _Mapping]]] = ..., post_scripts: _Optional[_Iterable[_Union[Script, _Mapping]]] = ..., certificate_id: _Optional[str] = ...) -> None: ...

class Assertion(_message.Message):
    __slots__ = ("target", "path", "op", "expected")
    TARGET_FIELD_NUMBER: _ClassVar[int]
    PATH_FIELD_NUMBER: _ClassVar[int]
    OP_FIELD_NUMBER: _ClassVar[int]
    EXPECTED_FIELD_NUMBER: _ClassVar[int]
    target: AssertionTarget
    path: str
    op: AssertionOp
    expected: str
    def __init__(self, target: _Optional[_Union[AssertionTarget, str]] = ..., path: _Optional[str] = ..., op: _Optional[_Union[AssertionOp, str]] = ..., expected: _Optional[str] = ...) -> None: ...

class AssertionResult(_message.Message):
    __slots__ = ("assertion", "passed", "actual", "message")
    ASSERTION_FIELD_NUMBER: _ClassVar[int]
    PASSED_FIELD_NUMBER: _ClassVar[int]
    ACTUAL_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    assertion: Assertion
    passed: bool
    actual: str
    message: str
    def __init__(self, assertion: _Optional[_Union[Assertion, _Mapping]] = ..., passed: _Optional[bool] = ..., actual: _Optional[str] = ..., message: _Optional[str] = ...) -> None: ...

class HttpOverride(_message.Message):
    __slots__ = ("method", "uri", "headers", "params", "body")
    METHOD_FIELD_NUMBER: _ClassVar[int]
    URI_FIELD_NUMBER: _ClassVar[int]
    HEADERS_FIELD_NUMBER: _ClassVar[int]
    PARAMS_FIELD_NUMBER: _ClassVar[int]
    BODY_FIELD_NUMBER: _ClassVar[int]
    method: HttpMethod
    uri: str
    headers: _containers.RepeatedCompositeFieldContainer[KeyValue]
    params: _containers.RepeatedCompositeFieldContainer[KeyValue]
    body: BodySpec
    def __init__(self, method: _Optional[_Union[HttpMethod, str]] = ..., uri: _Optional[str] = ..., headers: _Optional[_Iterable[_Union[KeyValue, _Mapping]]] = ..., params: _Optional[_Iterable[_Union[KeyValue, _Mapping]]] = ..., body: _Optional[_Union[BodySpec, _Mapping]] = ...) -> None: ...

class ApiCallStep(_message.Message):
    __slots__ = ("api_id", "override", "inline")
    API_ID_FIELD_NUMBER: _ClassVar[int]
    OVERRIDE_FIELD_NUMBER: _ClassVar[int]
    INLINE_FIELD_NUMBER: _ClassVar[int]
    api_id: str
    override: HttpOverride
    inline: HttpApi
    def __init__(self, api_id: _Optional[str] = ..., override: _Optional[_Union[HttpOverride, _Mapping]] = ..., inline: _Optional[_Union[HttpApi, _Mapping]] = ...) -> None: ...

class GrpcCallStep(_message.Message):
    __slots__ = ("grpc_api_id", "request_override", "metadata_override")
    GRPC_API_ID_FIELD_NUMBER: _ClassVar[int]
    REQUEST_OVERRIDE_FIELD_NUMBER: _ClassVar[int]
    METADATA_OVERRIDE_FIELD_NUMBER: _ClassVar[int]
    grpc_api_id: str
    request_override: _struct_pb2.Struct
    metadata_override: _containers.RepeatedCompositeFieldContainer[KeyValue]
    def __init__(self, grpc_api_id: _Optional[str] = ..., request_override: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., metadata_override: _Optional[_Iterable[_Union[KeyValue, _Mapping]]] = ...) -> None: ...

class AssertionStep(_message.Message):
    __slots__ = ("assertions",)
    ASSERTIONS_FIELD_NUMBER: _ClassVar[int]
    assertions: _containers.RepeatedCompositeFieldContainer[Assertion]
    def __init__(self, assertions: _Optional[_Iterable[_Union[Assertion, _Mapping]]] = ...) -> None: ...

class SetVarStep(_message.Message):
    __slots__ = ("key", "value_expr")
    KEY_FIELD_NUMBER: _ClassVar[int]
    VALUE_EXPR_FIELD_NUMBER: _ClassVar[int]
    key: str
    value_expr: str
    def __init__(self, key: _Optional[str] = ..., value_expr: _Optional[str] = ...) -> None: ...

class IfStep(_message.Message):
    __slots__ = ("condition_expr", "then_steps", "else_steps")
    CONDITION_EXPR_FIELD_NUMBER: _ClassVar[int]
    THEN_STEPS_FIELD_NUMBER: _ClassVar[int]
    ELSE_STEPS_FIELD_NUMBER: _ClassVar[int]
    condition_expr: str
    then_steps: _containers.RepeatedCompositeFieldContainer[TestStep]
    else_steps: _containers.RepeatedCompositeFieldContainer[TestStep]
    def __init__(self, condition_expr: _Optional[str] = ..., then_steps: _Optional[_Iterable[_Union[TestStep, _Mapping]]] = ..., else_steps: _Optional[_Iterable[_Union[TestStep, _Mapping]]] = ...) -> None: ...

class IntRange(_message.Message):
    __slots__ = ("start", "end")
    START_FIELD_NUMBER: _ClassVar[int]
    END_FIELD_NUMBER: _ClassVar[int]
    start: int
    end: int
    def __init__(self, start: _Optional[int] = ..., end: _Optional[int] = ...) -> None: ...

class LoopStep(_message.Message):
    __slots__ = ("iterator", "count", "range", "parallel", "body_steps")
    ITERATOR_FIELD_NUMBER: _ClassVar[int]
    COUNT_FIELD_NUMBER: _ClassVar[int]
    RANGE_FIELD_NUMBER: _ClassVar[int]
    PARALLEL_FIELD_NUMBER: _ClassVar[int]
    BODY_STEPS_FIELD_NUMBER: _ClassVar[int]
    iterator: str
    count: int
    range: IntRange
    parallel: bool
    body_steps: _containers.RepeatedCompositeFieldContainer[TestStep]
    def __init__(self, iterator: _Optional[str] = ..., count: _Optional[int] = ..., range: _Optional[_Union[IntRange, _Mapping]] = ..., parallel: _Optional[bool] = ..., body_steps: _Optional[_Iterable[_Union[TestStep, _Mapping]]] = ...) -> None: ...

class RetryStep(_message.Message):
    __slots__ = ("body_step", "max_attempts", "backoff")
    BODY_STEP_FIELD_NUMBER: _ClassVar[int]
    MAX_ATTEMPTS_FIELD_NUMBER: _ClassVar[int]
    BACKOFF_FIELD_NUMBER: _ClassVar[int]
    body_step: TestStep
    max_attempts: int
    backoff: _duration_pb2.Duration
    def __init__(self, body_step: _Optional[_Union[TestStep, _Mapping]] = ..., max_attempts: _Optional[int] = ..., backoff: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ...) -> None: ...

class CodeBlockStep(_message.Message):
    __slots__ = ("lang", "source")
    LANG_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    lang: str
    source: str
    def __init__(self, lang: _Optional[str] = ..., source: _Optional[str] = ...) -> None: ...

class DelayStep(_message.Message):
    __slots__ = ("duration",)
    DURATION_FIELD_NUMBER: _ClassVar[int]
    duration: _duration_pb2.Duration
    def __init__(self, duration: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ...) -> None: ...

class UiActionStep(_message.Message):
    __slots__ = ("action", "target", "value")
    ACTION_FIELD_NUMBER: _ClassVar[int]
    TARGET_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    action: UiAction
    target: str
    value: str
    def __init__(self, action: _Optional[_Union[UiAction, str]] = ..., target: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...

class TestStep(_message.Message):
    __slots__ = ("id", "type", "name", "api_call", "grpc_call", "assertion", "set_var", "if_step", "loop_step", "retry_step", "code_block", "delay", "ui_action")
    ID_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    API_CALL_FIELD_NUMBER: _ClassVar[int]
    GRPC_CALL_FIELD_NUMBER: _ClassVar[int]
    ASSERTION_FIELD_NUMBER: _ClassVar[int]
    SET_VAR_FIELD_NUMBER: _ClassVar[int]
    IF_STEP_FIELD_NUMBER: _ClassVar[int]
    LOOP_STEP_FIELD_NUMBER: _ClassVar[int]
    RETRY_STEP_FIELD_NUMBER: _ClassVar[int]
    CODE_BLOCK_FIELD_NUMBER: _ClassVar[int]
    DELAY_FIELD_NUMBER: _ClassVar[int]
    UI_ACTION_FIELD_NUMBER: _ClassVar[int]
    id: str
    type: StepType
    name: str
    api_call: ApiCallStep
    grpc_call: GrpcCallStep
    assertion: AssertionStep
    set_var: SetVarStep
    if_step: IfStep
    loop_step: LoopStep
    retry_step: RetryStep
    code_block: CodeBlockStep
    delay: DelayStep
    ui_action: UiActionStep
    def __init__(self, id: _Optional[str] = ..., type: _Optional[_Union[StepType, str]] = ..., name: _Optional[str] = ..., api_call: _Optional[_Union[ApiCallStep, _Mapping]] = ..., grpc_call: _Optional[_Union[GrpcCallStep, _Mapping]] = ..., assertion: _Optional[_Union[AssertionStep, _Mapping]] = ..., set_var: _Optional[_Union[SetVarStep, _Mapping]] = ..., if_step: _Optional[_Union[IfStep, _Mapping]] = ..., loop_step: _Optional[_Union[LoopStep, _Mapping]] = ..., retry_step: _Optional[_Union[RetryStep, _Mapping]] = ..., code_block: _Optional[_Union[CodeBlockStep, _Mapping]] = ..., delay: _Optional[_Union[DelayStep, _Mapping]] = ..., ui_action: _Optional[_Union[UiActionStep, _Mapping]] = ...) -> None: ...

class DeclarativeCase(_message.Message):
    __slots__ = ("steps",)
    STEPS_FIELD_NUMBER: _ClassVar[int]
    steps: _containers.RepeatedCompositeFieldContainer[TestStep]
    def __init__(self, steps: _Optional[_Iterable[_Union[TestStep, _Mapping]]] = ...) -> None: ...

class LowCodeCase(_message.Message):
    __slots__ = ("script_ref", "source", "entry", "parameters")
    SCRIPT_REF_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    ENTRY_FIELD_NUMBER: _ClassVar[int]
    PARAMETERS_FIELD_NUMBER: _ClassVar[int]
    script_ref: str
    source: str
    entry: str
    parameters: _struct_pb2.Struct
    def __init__(self, script_ref: _Optional[str] = ..., source: _Optional[str] = ..., entry: _Optional[str] = ..., parameters: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ...) -> None: ...

class TestCase(_message.Message):
    __slots__ = ("id", "tenant_id", "project_id", "type", "name", "description", "declarative", "lowcode", "tags", "created_by")
    ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    DECLARATIVE_FIELD_NUMBER: _ClassVar[int]
    LOWCODE_FIELD_NUMBER: _ClassVar[int]
    TAGS_FIELD_NUMBER: _ClassVar[int]
    CREATED_BY_FIELD_NUMBER: _ClassVar[int]
    id: str
    tenant_id: int
    project_id: str
    type: TestCaseType
    name: str
    description: str
    declarative: DeclarativeCase
    lowcode: LowCodeCase
    tags: _containers.RepeatedScalarFieldContainer[str]
    created_by: str
    def __init__(self, id: _Optional[str] = ..., tenant_id: _Optional[int] = ..., project_id: _Optional[str] = ..., type: _Optional[_Union[TestCaseType, str]] = ..., name: _Optional[str] = ..., description: _Optional[str] = ..., declarative: _Optional[_Union[DeclarativeCase, _Mapping]] = ..., lowcode: _Optional[_Union[LowCodeCase, _Mapping]] = ..., tags: _Optional[_Iterable[str]] = ..., created_by: _Optional[str] = ...) -> None: ...

class TestSuite(_message.Message):
    __slots__ = ("id", "tenant_id", "project_id", "name", "description", "case_ids")
    ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    CASE_IDS_FIELD_NUMBER: _ClassVar[int]
    id: str
    tenant_id: int
    project_id: str
    name: str
    description: str
    case_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, id: _Optional[str] = ..., tenant_id: _Optional[int] = ..., project_id: _Optional[str] = ..., name: _Optional[str] = ..., description: _Optional[str] = ..., case_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class NotificationRule(_message.Message):
    __slots__ = ("channels", "recipients", "on_success", "on_failure", "webhook_url")
    CHANNELS_FIELD_NUMBER: _ClassVar[int]
    RECIPIENTS_FIELD_NUMBER: _ClassVar[int]
    ON_SUCCESS_FIELD_NUMBER: _ClassVar[int]
    ON_FAILURE_FIELD_NUMBER: _ClassVar[int]
    WEBHOOK_URL_FIELD_NUMBER: _ClassVar[int]
    channels: _containers.RepeatedScalarFieldContainer[str]
    recipients: _containers.RepeatedScalarFieldContainer[str]
    on_success: bool
    on_failure: bool
    webhook_url: str
    def __init__(self, channels: _Optional[_Iterable[str]] = ..., recipients: _Optional[_Iterable[str]] = ..., on_success: _Optional[bool] = ..., on_failure: _Optional[bool] = ..., webhook_url: _Optional[str] = ...) -> None: ...

class PlanItem(_message.Message):
    __slots__ = ("case_id", "suite_id", "enabled", "param_overrides")
    CASE_ID_FIELD_NUMBER: _ClassVar[int]
    SUITE_ID_FIELD_NUMBER: _ClassVar[int]
    ENABLED_FIELD_NUMBER: _ClassVar[int]
    PARAM_OVERRIDES_FIELD_NUMBER: _ClassVar[int]
    case_id: str
    suite_id: str
    enabled: bool
    param_overrides: _struct_pb2.Struct
    def __init__(self, case_id: _Optional[str] = ..., suite_id: _Optional[str] = ..., enabled: _Optional[bool] = ..., param_overrides: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ...) -> None: ...

class TestPlan(_message.Message):
    __slots__ = ("id", "tenant_id", "project_id", "env_id", "name", "items", "concurrency", "retry_on_failure", "overlap_policy", "schedule_cron", "timeout", "notifications")
    ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    ENV_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    ITEMS_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    RETRY_ON_FAILURE_FIELD_NUMBER: _ClassVar[int]
    OVERLAP_POLICY_FIELD_NUMBER: _ClassVar[int]
    SCHEDULE_CRON_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    NOTIFICATIONS_FIELD_NUMBER: _ClassVar[int]
    id: str
    tenant_id: int
    project_id: str
    env_id: str
    name: str
    items: _containers.RepeatedCompositeFieldContainer[PlanItem]
    concurrency: int
    retry_on_failure: bool
    overlap_policy: OverlapPolicy
    schedule_cron: str
    timeout: _duration_pb2.Duration
    notifications: NotificationRule
    def __init__(self, id: _Optional[str] = ..., tenant_id: _Optional[int] = ..., project_id: _Optional[str] = ..., env_id: _Optional[str] = ..., name: _Optional[str] = ..., items: _Optional[_Iterable[_Union[PlanItem, _Mapping]]] = ..., concurrency: _Optional[int] = ..., retry_on_failure: _Optional[bool] = ..., overlap_policy: _Optional[_Union[OverlapPolicy, str]] = ..., schedule_cron: _Optional[str] = ..., timeout: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., notifications: _Optional[_Union[NotificationRule, _Mapping]] = ...) -> None: ...

class RunSummary(_message.Message):
    __slots__ = ("total", "passed", "failed", "skipped")
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    PASSED_FIELD_NUMBER: _ClassVar[int]
    FAILED_FIELD_NUMBER: _ClassVar[int]
    SKIPPED_FIELD_NUMBER: _ClassVar[int]
    total: int
    passed: int
    failed: int
    skipped: int
    def __init__(self, total: _Optional[int] = ..., passed: _Optional[int] = ..., failed: _Optional[int] = ..., skipped: _Optional[int] = ...) -> None: ...

class TestRun(_message.Message):
    __slots__ = ("id", "tenant_id", "plan_id", "env_id", "status", "trigger", "triggered_by", "started_at", "finished_at", "summary")
    ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PLAN_ID_FIELD_NUMBER: _ClassVar[int]
    ENV_ID_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    TRIGGER_FIELD_NUMBER: _ClassVar[int]
    TRIGGERED_BY_FIELD_NUMBER: _ClassVar[int]
    STARTED_AT_FIELD_NUMBER: _ClassVar[int]
    FINISHED_AT_FIELD_NUMBER: _ClassVar[int]
    SUMMARY_FIELD_NUMBER: _ClassVar[int]
    id: str
    tenant_id: int
    plan_id: str
    env_id: str
    status: RunStatus
    trigger: TriggerType
    triggered_by: str
    started_at: _timestamp_pb2.Timestamp
    finished_at: _timestamp_pb2.Timestamp
    summary: RunSummary
    def __init__(self, id: _Optional[str] = ..., tenant_id: _Optional[int] = ..., plan_id: _Optional[str] = ..., env_id: _Optional[str] = ..., status: _Optional[_Union[RunStatus, str]] = ..., trigger: _Optional[_Union[TriggerType, str]] = ..., triggered_by: _Optional[str] = ..., started_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., finished_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., summary: _Optional[_Union[RunSummary, _Mapping]] = ...) -> None: ...

class TestCaseResult(_message.Message):
    __slots__ = ("id", "run_id", "case_id", "status", "duration", "error")
    ID_FIELD_NUMBER: _ClassVar[int]
    RUN_ID_FIELD_NUMBER: _ClassVar[int]
    CASE_ID_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    DURATION_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    id: str
    run_id: str
    case_id: str
    status: CaseStatus
    duration: _duration_pb2.Duration
    error: str
    def __init__(self, id: _Optional[str] = ..., run_id: _Optional[str] = ..., case_id: _Optional[str] = ..., status: _Optional[_Union[CaseStatus, str]] = ..., duration: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., error: _Optional[str] = ...) -> None: ...

class ArtifactRef(_message.Message):
    __slots__ = ("id", "kind", "uri", "size")
    ID_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    URI_FIELD_NUMBER: _ClassVar[int]
    SIZE_FIELD_NUMBER: _ClassVar[int]
    id: str
    kind: str
    uri: str
    size: int
    def __init__(self, id: _Optional[str] = ..., kind: _Optional[str] = ..., uri: _Optional[str] = ..., size: _Optional[int] = ...) -> None: ...

class TestStepResult(_message.Message):
    __slots__ = ("id", "case_result_id", "step_path", "status", "duration", "request", "response", "assertions", "logs", "artifacts")
    ID_FIELD_NUMBER: _ClassVar[int]
    CASE_RESULT_ID_FIELD_NUMBER: _ClassVar[int]
    STEP_PATH_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    DURATION_FIELD_NUMBER: _ClassVar[int]
    REQUEST_FIELD_NUMBER: _ClassVar[int]
    RESPONSE_FIELD_NUMBER: _ClassVar[int]
    ASSERTIONS_FIELD_NUMBER: _ClassVar[int]
    LOGS_FIELD_NUMBER: _ClassVar[int]
    ARTIFACTS_FIELD_NUMBER: _ClassVar[int]
    id: str
    case_result_id: str
    step_path: str
    status: StepStatus
    duration: _duration_pb2.Duration
    request: _struct_pb2.Struct
    response: _struct_pb2.Struct
    assertions: _containers.RepeatedCompositeFieldContainer[AssertionResult]
    logs: _containers.RepeatedScalarFieldContainer[str]
    artifacts: _containers.RepeatedCompositeFieldContainer[ArtifactRef]
    def __init__(self, id: _Optional[str] = ..., case_result_id: _Optional[str] = ..., step_path: _Optional[str] = ..., status: _Optional[_Union[StepStatus, str]] = ..., duration: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., request: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., response: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., assertions: _Optional[_Iterable[_Union[AssertionResult, _Mapping]]] = ..., logs: _Optional[_Iterable[str]] = ..., artifacts: _Optional[_Iterable[_Union[ArtifactRef, _Mapping]]] = ...) -> None: ...

class RampStage(_message.Message):
    __slots__ = ("at", "target")
    AT_FIELD_NUMBER: _ClassVar[int]
    TARGET_FIELD_NUMBER: _ClassVar[int]
    at: _duration_pb2.Duration
    target: int
    def __init__(self, at: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., target: _Optional[int] = ...) -> None: ...

class LoadProfile(_message.Message):
    __slots__ = ("ramp", "duration", "concurrency_per_worker")
    RAMP_FIELD_NUMBER: _ClassVar[int]
    DURATION_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_PER_WORKER_FIELD_NUMBER: _ClassVar[int]
    ramp: _containers.RepeatedCompositeFieldContainer[RampStage]
    duration: _duration_pb2.Duration
    concurrency_per_worker: int
    def __init__(self, ramp: _Optional[_Iterable[_Union[RampStage, _Mapping]]] = ..., duration: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ..., concurrency_per_worker: _Optional[int] = ...) -> None: ...

class StressTestPlan(_message.Message):
    __slots__ = ("id", "tenant_id", "project_id", "env_id", "api_id", "behavior_case_id", "load_profile", "worker_count", "metrics_interval")
    ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PROJECT_ID_FIELD_NUMBER: _ClassVar[int]
    ENV_ID_FIELD_NUMBER: _ClassVar[int]
    API_ID_FIELD_NUMBER: _ClassVar[int]
    BEHAVIOR_CASE_ID_FIELD_NUMBER: _ClassVar[int]
    LOAD_PROFILE_FIELD_NUMBER: _ClassVar[int]
    WORKER_COUNT_FIELD_NUMBER: _ClassVar[int]
    METRICS_INTERVAL_FIELD_NUMBER: _ClassVar[int]
    id: str
    tenant_id: int
    project_id: str
    env_id: str
    api_id: str
    behavior_case_id: str
    load_profile: LoadProfile
    worker_count: int
    metrics_interval: _duration_pb2.Duration
    def __init__(self, id: _Optional[str] = ..., tenant_id: _Optional[int] = ..., project_id: _Optional[str] = ..., env_id: _Optional[str] = ..., api_id: _Optional[str] = ..., behavior_case_id: _Optional[str] = ..., load_profile: _Optional[_Union[LoadProfile, _Mapping]] = ..., worker_count: _Optional[int] = ..., metrics_interval: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ...) -> None: ...

class StressMetricPoint(_message.Message):
    __slots__ = ("ts", "rps", "latency_p50_ms", "latency_p95_ms", "latency_p99_ms", "error_rate", "concurrency")
    TS_FIELD_NUMBER: _ClassVar[int]
    RPS_FIELD_NUMBER: _ClassVar[int]
    LATENCY_P50_MS_FIELD_NUMBER: _ClassVar[int]
    LATENCY_P95_MS_FIELD_NUMBER: _ClassVar[int]
    LATENCY_P99_MS_FIELD_NUMBER: _ClassVar[int]
    ERROR_RATE_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    ts: _timestamp_pb2.Timestamp
    rps: float
    latency_p50_ms: float
    latency_p95_ms: float
    latency_p99_ms: float
    error_rate: float
    concurrency: int
    def __init__(self, ts: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., rps: _Optional[float] = ..., latency_p50_ms: _Optional[float] = ..., latency_p95_ms: _Optional[float] = ..., latency_p99_ms: _Optional[float] = ..., error_rate: _Optional[float] = ..., concurrency: _Optional[int] = ...) -> None: ...
