"""Minimal Linux wait4 supervisor; invoked only by benchmark.run_process.

Run with -I -S so the measured fork starts from a fresh, small address space.
Importing the benchmark harness here would reintroduce its allocation history.
The child inherits only this launcher's resident pages before its own exec,
the same launch-memory floor that applies to native timing supervisors.
"""

import os
import sys
import time


def main():
    destination = int(sys.argv[1])
    timeout = float(sys.argv[2])
    arguments = sys.argv[3:]
    # Only the supervisor writes metrics. The exec-error pipe closes atomically
    # on successful exec and reports errno when the executable cannot start.
    os.set_inheritable(destination, False)
    error_read, error_write = os.pipe2(os.O_CLOEXEC)
    started = time.perf_counter()
    child = os.fork()
    if child == 0:
        os.close(destination)
        os.close(error_read)
        try:
            os.execvpe(arguments[0], arguments, os.environ)
        except OSError as error:
            os.write(error_write, str(error.errno).encode("ascii"))
        finally:
            os._exit(127)
    os.close(error_write)
    timed_out = False
    try:
        error_data = os.read(error_read, 32)
        while True:
            pid, status, usage = os.wait4(child, os.WNOHANG)
            if pid:
                break
            remaining = timeout - (time.perf_counter() - started)
            if remaining <= 0:
                timed_out = True
                try:
                    os.kill(child, 9)
                except ProcessLookupError:
                    pass
                _, status, usage = os.wait4(child, 0)
                break
            time.sleep(min(0.001, remaining))
    except BaseException:
        try:
            os.kill(child, 9)
        except ProcessLookupError:
            pass
        os.wait4(child, 0)
        raise
    finally:
        os.close(error_read)
    elapsed = (time.perf_counter() - started) * 1000
    cpu = (usage.ru_utime + usage.ru_stime) * 1000
    # Avoid importing a JSON encoder into this deliberately minimal launcher.
    payload = ('{"schema":1,"status":%d,"wall_ms":%.9f,"process_cpu_ms":%.9f,'
               '"peak_rss_bytes":%d,"timed_out":%s,"exec_errno":%s}\n') % (
                   status, elapsed, cpu, usage.ru_maxrss * 1024,
                   "true" if timed_out else "false", error_data.decode("ascii") if error_data else "null")
    os.write(destination, payload.encode("ascii"))


if __name__ == "__main__":
    main()
