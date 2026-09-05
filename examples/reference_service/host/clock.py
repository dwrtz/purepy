"""Explicit authority to suspend an invocation using the host event loop."""

import asyncio


class ClockWait:
    __slots__ = ()


async def sleep(clock_wait: ClockWait, seconds: float) -> None:
    if type(clock_wait) is not ClockWait:
        raise TypeError("ClockWait capability required")
    await asyncio.sleep(seconds)
