"""Async and generator edge cases: await, async with / async for, generators
(yield / yield from), and async generators.
"""


class Resource:
    """An async context manager."""

    async def __aenter__(self) -> "Resource":
        return self

    async def __aexit__(self, *exc) -> None:
        ...

    def close(self) -> None:
        ...


async def fetch() -> int:
    """A leaf coroutine."""
    return 1


async def consume_await() -> int:
    """IDEAL: await fetch() -> calls fetch."""
    return await fetch()


async def consume_async_with() -> None:
    """IDEAL: async with Resource() -> uses_class Resource; r.close() -> calls close."""
    async with Resource() as r:
        r.close()


async def agen():
    """An async generator (IDEAL: flagged async)."""
    yield 1
    yield 2


async def consume_async_for() -> int:
    """IDEAL: async for over agen() -> calls agen."""
    total = 0
    async for v in agen():
        total += v
    return total


def number_gen():
    """A generator function (IDEAL: recognized as a generator)."""
    yield 1
    yield 2


def delegating_gen():
    """IDEAL: yield from number_gen() -> calls number_gen."""
    yield from number_gen()
