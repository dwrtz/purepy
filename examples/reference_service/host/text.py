"""This exact UTF-8 conversion is declared trusted pure in the manifest."""


def encode_utf8(text: str) -> bytes:
    return text.encode("utf-8")
