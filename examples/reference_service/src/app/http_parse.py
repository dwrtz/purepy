from app.types import Request


def header_end(data: bytes) -> int:
    for offset in range(len(data) - 3):
        if data[offset:offset + 4] == b"\r\n\r\n":
            return offset + 4
    return 0


def parse_request(data: bytes) -> Request:
    end = header_end(data)
    if end == 0:
        return Request(False, b"", b"")
    first_space = -1
    second_space = -1
    line_end = -1
    for offset in range(end - 1):
        if data[offset:offset + 2] == b"\r\n":
            line_end = offset
            break
        if data[offset] == 32:
            if first_space == -1:
                first_space = offset
            else:
                if second_space == -1:
                    second_space = offset
                else:
                    return Request(False, b"", b"")
    if first_space <= 0 or second_space <= first_space + 1 or line_end <= second_space:
        return Request(False, b"", b"")
    if data[second_space + 1:line_end] != b"HTTP/1.1":
        return Request(False, b"", b"")
    method = data[:first_space]
    if method != b"GET" and method != b"POST":
        return Request(False, b"", b"")
    path = data[first_space + 1:second_space]
    if path[:1] != b"/":
        return Request(False, b"", b"")
    for character in path:
        if character < 33 or character > 126:
            return Request(False, b"", b"")
    return Request(True, method, path)


def decimal(data: bytes) -> int:
    if len(data) == 0 or len(data) > 9:
        return -1
    result = 0
    for character in data:
        if character < 48 or character > 57:
            return -1
        result = result * 10 + character - 48
    return result
