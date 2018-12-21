from typing import Any, Tuple, List, Union, Callable
from .client import Client
import os
from abc import *


# Convert python int to bytes of golang big.Int.
def int_to_bytes(v: int) -> bytes:
    if v == 0:
        return b''
    n_bytes = ((v + (v < 0)).bit_length() + 8) // 8
    return v.to_bytes(n_bytes, byteorder="big", signed=True)


# Convert bytes of golang big.Int to python int.
def bytes_to_int(v: bytes) -> int:
    return int.from_bytes(v, "big", signed=True)


class Message(object):
    VERSION = 0
    INVOKE = 1
    RESULT = 2
    GETVALUE = 3
    SETVALUE = 4
    CALL = 5
    EVENT = 6
    GETINFO = 7
    GETBALANCE = 8
    GETAPI = 9


class Status(object):
    SUCCESS = 0
    SYSTEM_FAILURE = 1


class Codec(metaclass=ABCMeta):
    @abstractmethod
    def encode(self, o: Any) -> Tuple[int, bytes]:
        pass

    @abstractmethod
    def decode(self, t: int, bs: bytes) -> Any:
        pass


class TypeTag(object):
    NIL = 0
    DICT = 1
    LIST = 2
    BYTES = 3
    STRING = 4

    CUSTOM = 10
    INT = CUSTOM + 1
    ADDRESS = CUSTOM


class ServiceManagerProxy:
    def __init__(self):
        self.__client = Client()
        self.__invoke = None
        self.__get_api = None
        self.__codec = None
        self.__readonly_stack = []
        self.__readonly = False

    def connect(self, addr):
        self.__client.connect(addr)

    def send_version(self, v: int, pid: int, name: str):
        self.__client.send(Message.VERSION, [
            v,
            pid,
            name,
        ])

    def set_invoke_handler(self, invoke: Callable[[str, 'Address', 'Address', int, int, str, Any], None]):
        self.__invoke = invoke

    def set_api_handler(self, api: Callable[[str], Any]):
        self.__get_api = api

    def set_codec(self, codec: Codec) -> None:
        self.__codec = codec

    def decode(self, tag: int, val: bytes) -> 'Any':
        if tag == TypeTag.BYTES:
            return val
        elif tag == TypeTag.STRING:
            return val.decode('utf-8')
        elif tag == TypeTag.INT:
            return bytes_to_int(val)
        else:
            return self.__codec.decode(tag, val)

    def encode(self, o: Any) -> Tuple[bytes]:
        if o is None:
            return bytes([])
        if isinstance(o, int):
            return int_to_bytes(o)
        elif isinstance(o, str):
            return o.encode('utf-8')
        elif isinstance(o, bytes):
            return o
        else:
            t, v = self.__codec.encode(o)
            return v

    def decode_any(self, to: list) -> Any:
        tag: int = to[0]
        val: Union[bytes, dict, list] = to[1]
        if tag == TypeTag.NIL:
            return None
        elif tag == TypeTag.DICT:
            obj = {}
            for k, v in val.items():
                if isinstance(k, bytes):
                    k = k.decode('utf-8')
                obj[k] = self.decode_any(v)
            return obj
        elif tag == TypeTag.LIST:
            obj = []
            for v in val:
                obj.append(self.decode_any(v))
            return obj
        else:
            return self.decode(tag, val)

    def encode_any(self, o: Any) -> Tuple[int,Any]:
        if o is None:
            return TypeTag.NIL, b''
        elif isinstance(o, dict):
            m = {}
            for k, v in o.items():
                m[k] = self.encode_any(v)
            return TypeTag.DICT, m
        elif isinstance(o, list):
            lst = []
            for v in o:
                lst.append(self.encode_any(v))
            return TypeTag.LIST, lst
        elif isinstance(o, bytes):
            return TypeTag.BYTES, o
        elif isinstance(o, str):
            return TypeTag.STRING, o.encode('utf-8')
        elif isinstance(o, int):
            return TypeTag.INT, int_to_bytes(o)
        else:
            return self.__codec.encode(o)

    def __handle_invoke(self, data):
        code = self.decode(TypeTag.STRING, data[0])
        is_query = data[2]
        _from = self.decode(TypeTag.ADDRESS, data[2])
        _to = self.decode(TypeTag.ADDRESS, data[3])
        value = self.decode(TypeTag.INT, data[4])
        limit = self.decode(TypeTag.INT, data[5])
        method = self.decode(TypeTag.STRING, data[6])
        params = data[7]

        try:
            self.__readonly_stack.append(self.__readonly)
            self.__readonly = is_query
            status, step_used, result = self.__invoke(
                code, _from, _to, value, limit, method, params)

            self.__client.send(Message.RESULT, [
                status,
                self.encode(step_used),
                self.encode_any(result)
            ])
        except BaseException as e:
            self.__client.send(Message.RESULT, [
                Status.SYSTEM_FAILURE,
                self.encode(limit),
                self.encode_any(None)
            ])
        finally:
            self.__readonly = self.__readonly_stack.pop(-1)

    def __handle_get_api(self, data):
        try:
            obj = self.__get_api(str(data))
            self.__client.send(Message.GETAPI, self.encode_any(obj))
        except:
            self.__client.send(Message.GETAPI, [
                self.encode_any(None)
            ])

    def loop(self):
        while True:
            msg, data = self.__client.receive()
            if msg == Message.INVOKE:
                self.__handle_invoke(data)
            elif msg == Message.GETAPI:
                self.__handle_get_api(data)

    def call(self, to: 'Address', value: int,
             step_limit: int, method: str,
             params: bytes) -> Tuple[int, int, Any]:

        self.__client.send(Message.CALL, [
            self.encode(to), self.encode(value), self.encode(step_limit),
            self.encode(method), params,
        ])

        while True:
            msg, data = self.__client.receive()
            if msg == Message.INVOKE:
                self.__handle_invoke(data)
            elif msg == Message.RESULT:
                return data[0], self.decode(TypeTag.INT, data[1]), self.decode_any(data[2])

    def get_value(self, key: bytes) -> Union[None, bytes]:
        msg, value = self.__client.send_and_receive(Message.GETVALUE, key)
        if msg != Message.GETVALUE:
            raise Exception(f'InvalidMsg({msg}) exp={Message.GETVALUE}')
        if value[0]:
            return value[1]
        else:
            return None

    def set_value(self, key: bytes, value: Union[bytes, None]):
        if self.__readonly:
            raise Exception('NoPermissionToWrite')
        if value is None:
            self.__client.send(Message.SETVALUE, [key, True, b''])
        else:
            self.__client.send(Message.SETVALUE, [key, False, value])

    def get_info(self) -> Any:
        msg, value = self.__client.send_and_receive(Message.GETINFO, b'')
        if msg != Message.GETINFO:
            raise Exception(f'InvalidMsg({msg}) exp={Message.GETINFO}')
        return self.decode_any(value)

    def get_balance(self, addr: 'Address') -> int:
        msg, value = self.__client.send_and_receive(Message.GETBALANCE, self.encode(addr))
        if msg != Message.GETBALANCE:
            raise Exception(f'InvalidMsg({msg}) exp={Message.GETBALANCE}')
        return self.decode(TypeTag.INT, value)

    def send_event(self, indexed: List[Any], data: List[Any]):
        if self.__readonly:
            return
        self.__client.send(Message.EVENT, [
            [self.encode(v) for v in indexed],
            [self.encode(v) for v in data]
        ])
