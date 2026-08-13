from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Optional as _Optional

DESCRIPTOR: _descriptor.FileDescriptor

class EchoRequest(_message.Message):
    __slots__ = ("message", "repeat")
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    REPEAT_FIELD_NUMBER: _ClassVar[int]
    message: str
    repeat: int
    def __init__(self, message: _Optional[str] = ..., repeat: _Optional[int] = ...) -> None: ...

class EchoResponse(_message.Message):
    __slots__ = ("message", "length")
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    LENGTH_FIELD_NUMBER: _ClassVar[int]
    message: str
    length: int
    def __init__(self, message: _Optional[str] = ..., length: _Optional[int] = ...) -> None: ...

class AddRequest(_message.Message):
    __slots__ = ("a", "b")
    A_FIELD_NUMBER: _ClassVar[int]
    B_FIELD_NUMBER: _ClassVar[int]
    a: int
    b: int
    def __init__(self, a: _Optional[int] = ..., b: _Optional[int] = ...) -> None: ...

class AddResponse(_message.Message):
    __slots__ = ("sum",)
    SUM_FIELD_NUMBER: _ClassVar[int]
    sum: int
    def __init__(self, sum: _Optional[int] = ...) -> None: ...
