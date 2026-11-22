"""AISAI's Python debug driver.

It runs one program under the standard library's bdb and talks to the AISAI
backend over a loopback socket, one JSON object per line. Only the standard
library is used, so nothing has to be installed in the project.

The backend starts it as:

    python -u driver.py PROGRAM [ARGS...]

with AISAI_DEBUG_PORT and AISAI_DEBUG_TOKEN in the environment. Both are
removed before the program runs. The program's own stdout and stderr are left
alone: the backend reads them from the process.
"""

import bdb
import json
import os
import queue
import reprlib
import socket
import sys
import threading
import traceback

_DRIVER = os.path.abspath(__file__)
_BDB = os.path.abspath(bdb.__file__)
_MAX_VARS = 200

_repr = reprlib.Repr()
_repr.maxstring = 240
_repr.maxother = 240
_repr.maxlist = 20
_repr.maxdict = 20
_repr.maxset = 20
_repr.maxtuple = 20
_repr.maxlevel = 3


def _safe_repr(value):
    try:
        return _repr.repr(value)
    except Exception as exc:  # a broken __repr__ must not stop the session
        return "<repr failed: %s>" % type(exc).__name__


def _variables(mapping, module_level):
    out = []
    for name in sorted(mapping):
        if module_level and name.startswith("__") and name.endswith("__"):
            continue
        value = mapping[name]
        if module_level and type(value).__name__ == "module":
            continue
        out.append({"name": name, "type": type(value).__name__, "value": _safe_repr(value)})
        if len(out) >= _MAX_VARS:
            break
    return out


class Channel:
    """The line-oriented JSON connection to the backend."""

    def __init__(self, sock):
        self.sock = sock
        self.reader = sock.makefile("r", encoding="utf-8")
        self.lock = threading.Lock()

    def send(self, message):
        data = (json.dumps(message) + "\n").encode("utf-8")
        with self.lock:
            try:
                self.sock.sendall(data)
            except OSError:
                pass

    def messages(self):
        for line in self.reader:
            line = line.strip()
            if not line:
                continue
            try:
                yield json.loads(line)
            except ValueError:
                continue


class Debugger(bdb.Bdb):
    def __init__(self, channel, program, stop_on_entry):
        super().__init__()
        self.channel = channel
        self.program = self.canonic(program)
        self.stop_on_entry = stop_on_entry
        self.commands = queue.Queue()
        self.started = False
        self.pause_requested = False
        self.main_thread = threading.get_ident()

    # ------------------------------------------------------------ commands

    def listen(self):
        """Reads commands on a background thread.

        Breakpoints and pause take effect while the program runs; everything
        else is handed to the paused program through the queue.
        """
        for msg in self.channel.messages():
            cmd = msg.get("cmd")
            if cmd == "setBreakpoints":
                self.replace_breakpoints(msg.get("file", ""), msg.get("lines") or [])
            elif cmd == "pause":
                self.request_pause()
            else:
                self.commands.put(msg)
        # The backend went away: let the program finish undisturbed.
        self.commands.put({"cmd": "disconnect"})

    def replace_breakpoints(self, filename, lines):
        filename = self.canonic(filename)
        for line in list(self.get_file_breaks(filename)):
            self.clear_break(filename, line)
        verified = []
        for line in lines:
            try:
                line = int(line)
            except (TypeError, ValueError):
                continue
            if self.set_break(filename, line) is None:
                verified.append(line)
        self.channel.send({"event": "breakpoints", "file": filename, "lines": verified})

    def request_pause(self):
        self.pause_requested = True
        # Frames entered before the pause may not be traced line by line;
        # turning tracing on for the whole stack makes the next line stop.
        frame = sys._current_frames().get(self.main_thread)
        while frame is not None:
            frame.f_trace = self.trace_dispatch
            frame.f_trace_lines = True
            frame = frame.f_back

    # ------------------------------------------------------------ bdb hooks

    def set_continue(self):
        # bdb removes the trace function when there are no breakpoints. It is
        # kept, so a breakpoint set while the program runs still stops it.
        self._set_stopinfo(self.botframe, None, -1)

    def stop_here(self, frame):
        if self.pause_requested:
            return True
        return super().stop_here(frame)

    def user_call(self, frame, argument_list):
        pass

    def user_return(self, frame, return_value):
        pass

    def user_line(self, frame):
        if not self.started:
            # The first line of the program itself, after runpy's setup.
            if self.canonic(frame.f_code.co_filename) != self.program:
                return
            self.started = True
            if not self.stop_on_entry and not self.break_here(frame):
                self.set_continue()
                return
            self.interact(frame, None, "entry" if self.stop_on_entry else "breakpoint", "")
            return
        if self.pause_requested:
            self.pause_requested = False
            self.interact(frame, None, "pause", "")
            return
        reason = "breakpoint" if self.break_here(frame) else "step"
        self.interact(frame, None, reason, "")

    def user_exception(self, frame, exc_info):
        # Handled exceptions are left alone; only an uncaught one stops, and
        # that is reported by run() once the stack has unwound.
        pass

    # ------------------------------------------------------------ pausing

    def frames(self, frame, tb):
        stack, _ = self.get_stack(frame, tb)
        out = []
        for f, line in stack:
            filename = self.canonic(f.f_code.co_filename)
            if filename in (_DRIVER, _BDB) or filename.startswith("<"):
                continue
            if os.path.basename(filename) == "runpy.py":
                continue
            out.append((f, line))
        out.reverse()  # innermost first
        return out

    def describe(self, stack, index):
        frames = [
            {"file": self.canonic(f.f_code.co_filename), "line": line, "name": f.f_code.co_name}
            for f, line in stack
        ]
        frame = stack[index][0] if stack else None
        module_level = frame is not None and frame.f_code.co_name == "<module>"
        locals_ = _variables(frame.f_locals, module_level) if frame is not None else []
        globals_ = [] if module_level or frame is None else _variables(frame.f_globals, True)
        return frames, locals_, globals_

    def interact(self, frame, tb, reason, description):
        stack = self.frames(frame, tb)
        index = 0
        frames, locals_, globals_ = self.describe(stack, index)
        self.channel.send({
            "event": "stopped",
            "reason": reason,
            "description": description,
            "frames": frames,
            "frame": index,
            "locals": locals_,
            "globals": globals_,
        })
        while True:
            msg = self.commands.get()
            cmd = msg.get("cmd")
            if cmd == "frame":
                try:
                    index = max(0, min(int(msg.get("frame", 0)), len(stack) - 1))
                except (TypeError, ValueError):
                    index = 0
                frames, locals_, globals_ = self.describe(stack, index)
                self.channel.send({"event": "scope", "frame": index, "locals": locals_, "globals": globals_})
                continue
            if cmd == "evaluate":
                self.evaluate(stack, msg)
                continue
            if tb is not None:
                # After an uncaught exception there is nothing left to step.
                if cmd in ("continue", "next", "step", "out", "disconnect"):
                    self.channel.send({"event": "continued"})
                    return
                continue
            if cmd == "continue" or cmd == "disconnect":
                self.set_continue()
            elif cmd == "next":
                self.set_next(stack[index][0] if stack else frame)
            elif cmd == "step":
                self.set_step()
            elif cmd == "out":
                self.set_return(stack[index][0] if stack else frame)
            else:
                continue
            self.channel.send({"event": "continued"})
            return

    def evaluate(self, stack, msg):
        expression = str(msg.get("expression", ""))
        try:
            index = max(0, min(int(msg.get("frame", 0)), len(stack) - 1))
        except (TypeError, ValueError):
            index = 0
        reply = {"event": "evaluated", "expression": expression, "seq": msg.get("seq")}
        if not stack:
            reply["error"] = "nothing to evaluate in"
            self.channel.send(reply)
            return
        frame = stack[index][0]
        try:
            try:
                code = compile(expression, "<debug>", "eval")
            except SyntaxError:
                code = compile(expression, "<debug>", "exec")
            value = eval(code, frame.f_globals, frame.f_locals)
            reply["value"] = _safe_repr(value)
            reply["type"] = type(value).__name__
        except Exception:
            reply["error"] = "".join(traceback.format_exception_only(*sys.exc_info()[:2])).strip()
        self.channel.send(reply)

    # ------------------------------------------------------------ running

    def run_program(self, args):
        import runpy

        sys.argv = [self.program] + list(args)
        sys.path[0] = os.path.dirname(self.program)
        code = 0
        try:
            self.reset()
            sys.settrace(self.trace_dispatch)
            try:
                runpy.run_path(self.program, run_name="__main__")
            finally:
                sys.settrace(None)
        except SystemExit as exc:
            code = exc.code if isinstance(exc.code, int) else (0 if exc.code is None else 1)
        except BaseException as exc:
            code = 1
            text = "".join(traceback.format_exception(type(exc), exc, exc.__traceback__))
            sys.stderr.write(text)
            sys.stderr.flush()
            description = "".join(traceback.format_exception_only(type(exc), exc)).strip()
            if exc.__traceback__ is not None:
                self.interact(None, exc.__traceback__, "exception", description)
        return code


def main():
    if len(sys.argv) < 2:
        sys.stderr.write("usage: driver.py PROGRAM [ARGS...]\n")
        return 2
    port = int(os.environ.pop("AISAI_DEBUG_PORT", "0"))
    token = os.environ.pop("AISAI_DEBUG_TOKEN", "")
    stop_on_entry = os.environ.pop("AISAI_DEBUG_STOP_ON_ENTRY", "") == "1"
    sock = socket.create_connection(("127.0.0.1", port))
    channel = Channel(sock)
    channel.send({"event": "hello", "token": token, "python": sys.version.split()[0], "pid": os.getpid()})

    program = os.path.abspath(sys.argv[1])
    debugger = Debugger(channel, program, stop_on_entry)

    # The backend sends the breakpoints first and then "start".
    for msg in channel.messages():
        cmd = msg.get("cmd")
        if cmd == "setBreakpoints":
            debugger.replace_breakpoints(msg.get("file", ""), msg.get("lines") or [])
        elif cmd == "start":
            break
    else:
        return 0

    threading.Thread(target=debugger.listen, daemon=True).start()
    code = debugger.run_program(sys.argv[2:])
    sys.stdout.flush()
    sys.stderr.flush()
    channel.send({"event": "exited", "code": code})
    return code


if __name__ == "__main__":
    sys.exit(main())
