"""A live end-to-end harness: runs the real desk-agent binary in a pseudo-terminal, reads the screen through a terminal
emulator (pyte) and checks it. Nothing is mocked: the model is the one in the config, the MCP server is a real stdio
process. Run it with e2e/run.sh."""

import fcntl
import os
import pty
import re
import select
import signal
import struct
import termios
import time

import pyte

ENTER, CTRL_C, CTRL_D = "\r", "\x03", "\x04"
UP, DOWN, PGUP, PGDN = "\x1b[A", "\x1b[B", "\x1b[5~", "\x1b[6~"
TAB, ESC, ALT_ENTER = "\t", "\x1b", "\x1b\r"
WHEEL_UP, WHEEL_DOWN = "\x1b[<64;10;10M", "\x1b[<65;10;10M"


class Failure(AssertionError):
    pass


class Screen(pyte.Screen):
    """pyte lacks SU and SD (CSI n S and CSI n T), which the program's renderer uses to scroll part of the screen."""

    def _margins(self):
        return self.margins if self.margins else pyte.screens.Margins(0, self.lines - 1)

    def scroll_up(self, count=1):
        top, bottom = self._margins()
        for _ in range(count or 1):
            for y in range(top, bottom):
                self.buffer[y] = self.buffer[y + 1]
            self.buffer.pop(bottom, None)
        self.dirty.update(range(self.lines))

    def scroll_down(self, count=1):
        top, bottom = self._margins()
        for _ in range(count or 1):
            for y in range(bottom, top, -1):
                self.buffer[y] = self.buffer[y - 1]
            self.buffer.pop(top, None)
        self.dirty.update(range(self.lines))


class Stream(pyte.ByteStream):
    csi = {**pyte.ByteStream.csi, "S": "scroll_up", "T": "scroll_down"}


class Term:
    """The program, its pseudo-terminal and the screen the user would see."""

    def __init__(self, argv, env=None, rows=40, cols=120):
        self.rows, self.cols = rows, cols
        self.screen = Screen(cols, rows)
        self.stream = Stream(self.screen)
        self.raw = b""
        self.exit_code = None
        pid, fd = pty.fork()
        if pid == 0:
            # The window has its size before the program starts, as in a real terminal.
            fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
            os.environ.update({"TERM": "xterm-256color", **(env or {})})
            os.execv(argv[0], argv)
        self.pid, self.fd = pid, fd

    def _size(self, rows, cols):
        fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))

    def resize(self, rows, cols):
        self.rows, self.cols = rows, cols
        self.screen.resize(rows, cols)
        self._size(rows, cols)
        self.pump(0.5)

    def pump(self, seconds=0.2):
        """Reads what the program has drawn for a while and feeds it to the emulator."""
        end = time.time() + seconds
        while time.time() < end:
            if select.select([self.fd], [], [], 0.05)[0]:
                try:
                    data = os.read(self.fd, 65536)
                except OSError:
                    self._reap()
                    return
                if not data:
                    self._reap()
                    return
                self.raw += data
                # The kitty keyboard protocol (CSI > 1 u and the like) is not for the screen; pyte would draw its tail.
                self.stream.feed(re.sub(rb"\x1b\[[>=<?][0-9;]*u", b"", data))

    def _reap(self):
        if self.exit_code is None:
            try:
                pid, status = os.waitpid(self.pid, os.WNOHANG)
                if pid:
                    self.exit_code = os.waitstatus_to_exitcode(status)
            except ChildProcessError:
                pass

    def exited(self, timeout=3):
        """The exit code once the program has ended, or None if it is still running after timeout seconds."""
        end = time.time() + timeout
        while self.exit_code is None and time.time() < end:
            self.pump(0.1)
            self._reap()
        return self.exit_code

    def send(self, text, settle=0.3):
        os.write(self.fd, text.encode())
        self.pump(settle)

    def type(self, text, enter=True):
        self.send(text, 0.1)
        if enter:
            self.send(ENTER, 0.3)

    @property
    def lines(self):
        return [l.rstrip() for l in self.screen.display]

    @property
    def text(self):
        return "\n".join(self.lines)

    def wait_for(self, pattern, timeout=60, what=None):
        """Waits until the screen matches (a substring, or a compiled regex). Returns the match or raises."""
        rx = pattern if hasattr(pattern, "search") else re.compile(re.escape(pattern))
        end = time.time() + timeout
        while time.time() < end:
            self.pump(0.25)
            m = rx.search(self.text)
            if m:
                return m
        raise Failure(f"timed out after {timeout}s waiting for {what or rx.pattern!r}\n{self.dump()}")

    def wait_gone(self, pattern, timeout=60):
        end = time.time() + timeout
        while time.time() < end:
            self.pump(0.25)
            if pattern not in self.text:
                return
        raise Failure(f"{pattern!r} never went away\n{self.dump()}")

    def idle(self, timeout=120):
        """Waits for the turn to finish: no 'Working' line, and the screen has stopped changing."""
        end = time.time() + timeout
        last, stable = None, 0
        while time.time() < end:
            self.pump(0.4)
            snap = self.text
            busy = "Working" in snap or "Cancelling" in snap
            stable = stable + 1 if snap == last and not busy else 0
            last = snap
            if stable >= 3:
                return
        raise Failure(f"the turn never finished\n{self.dump()}")

    def dump(self):
        bar = "─" * self.cols
        return f"{bar}\n{self.text}\n{bar}"

    def expect(self, cond, message):
        if not cond:
            raise Failure(f"{message}\n{self.dump()}")

    def contains(self, text, message=None):
        self.expect(text in self.text, message or f"the screen does not show {text!r}")

    def lacks(self, text, message=None):
        self.expect(text not in self.text, message or f"the screen shows {text!r}")

    def close(self, quit_keys=CTRL_C + CTRL_C):
        if self.exit_code is None:
            try:
                os.write(self.fd, quit_keys.encode())
            except OSError:
                pass
            self.pump(1.0)
        if self.exit_code is None:
            try:
                os.kill(self.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            self.pump(0.3)
            self._reap()
        try:
            os.close(self.fd)
        except OSError:
            pass
