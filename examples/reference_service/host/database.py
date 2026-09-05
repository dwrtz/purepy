"""SQLite operations run in a worker thread; one update is one transaction.

Capabilities carry the selected database, so no operation consults a global pool.
No connection, cursor, lock, or transaction crosses the verified boundary.
"""

import asyncio
import sqlite3
import threading

from app.types import Event, EventPage, UpdatePlan, UpdateResult, User, UserResult


class Database:
    def __init__(self, path=":memory:", delay=0.0):
        self.connection = sqlite3.connect(path, check_same_thread=False)
        self.lock = threading.Lock()
        self.delay = delay
        self.active_reads = 0
        self.max_active_reads = 0
        self.connection.executescript("""
            CREATE TABLE IF NOT EXISTS users (
                user_id INTEGER PRIMARY KEY, name TEXT NOT NULL, balance INTEGER NOT NULL
            );
            CREATE TABLE IF NOT EXISTS events (
                event_id INTEGER PRIMARY KEY AUTOINCREMENT,
                user_id INTEGER NOT NULL, balance INTEGER NOT NULL
            );
            INSERT OR IGNORE INTO users VALUES (1, 'Ada', 0);
            INSERT OR IGNORE INTO users VALUES (2, 'Grace', 0);
        """)
        self.connection.commit()

    def close(self):
        # Also waits for an operation still running after its caller was cancelled.
        with self.lock:
            self.connection.close()

    def _load(self, identifier):
        with self.lock:
            row = self.connection.execute(
                "SELECT user_id, name, balance FROM users WHERE user_id = ?", (identifier,)
            ).fetchone()
            return UserResult(row is not None, User(*row) if row else User(0, "", 0), "")

    def _update(self, plan):
        with self.lock, self.connection:
            row = self.connection.execute(
                "UPDATE users SET balance = balance + ? WHERE user_id = ? RETURNING balance",
                (plan.amount, plan.user_id),
            ).fetchone()
            if row is None:
                return UpdateResult(False, 0, "")
            self.connection.execute(
                "INSERT INTO events (user_id, balance) VALUES (?, ?)", (plan.user_id, row[0])
            )
            return UpdateResult(True, row[0], "")

    def _events(self, cursor):
        with self.lock:
            rows = self.connection.execute(
                "SELECT event_id, user_id, balance FROM events WHERE event_id > ? ORDER BY event_id LIMIT 32",
                (cursor,),
            ).fetchall()
            return EventPage(tuple(Event(*row) for row in rows))


class DatabaseRead:
    __slots__ = ("database",)

    def __init__(self, database):
        self.database = database


class DatabaseWrite:
    __slots__ = ("database",)

    def __init__(self, database):
        self.database = database


async def _thread_operation(function, *arguments):
    """Keep a started database operation owned until its worker has finished."""
    worker = asyncio.create_task(asyncio.to_thread(function, *arguments))
    try:
        return await asyncio.shield(worker)
    except asyncio.CancelledError:
        # Cancellation belongs to the invocation, not the SQLite transaction.
        # Join the worker before invocation cleanup may close the database.
        try:
            await worker
        finally:
            raise


async def load_user(database_read: DatabaseRead, user_id: int) -> UserResult:
    if type(database_read) is not DatabaseRead:
        raise TypeError("DatabaseRead capability required")
    database = database_read.database
    database.active_reads += 1
    database.max_active_reads = max(database.max_active_reads, database.active_reads)
    try:
        await asyncio.sleep(database.delay)
        return await _thread_operation(database._load, user_id)
    except sqlite3.Error:
        return UserResult(False, User(0, "", 0), "database unavailable")
    finally:
        database.active_reads -= 1


async def execute_update(database_write: DatabaseWrite, plan: UpdatePlan) -> UpdateResult:
    if type(database_write) is not DatabaseWrite:
        raise TypeError("DatabaseWrite capability required")
    try:
        return await _thread_operation(database_write.database._update, plan)
    except sqlite3.Error:
        return UpdateResult(False, 0, "database unavailable")


async def load_event_page(database_read: DatabaseRead, cursor: int) -> EventPage:
    if type(database_read) is not DatabaseRead:
        raise TypeError("DatabaseRead capability required")
    # Failure terminates this invocation; it is not converted to an empty page.
    return await _thread_operation(database_read.database._events, cursor)
