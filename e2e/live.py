#!/usr/bin/env python3
"""Live end-to-end scenarios for chiron. See e2e/run.sh. Needs a reachable model (the one in your config, or
CHIRON_LIVE_BASE_URL and CHIRON_LIVE_MODEL), and pyte."""

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
import time
import traceback

sys.path.insert(0, os.path.dirname(__file__))
from harness import *  # noqa: E402,F401,F403

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
WORK = tempfile.mkdtemp(prefix="chiron-live-")
BIN, MCP = os.path.join(WORK, "chiron"), os.path.join(WORK, "mcp.test")
MANY_LINES = "\n".join(f"row {i}" for i in range(1, 41))


class Skip(Exception):
    pass


# --- setup ---------------------------------------------------------------------------------------------------

def build():
    for args in (["go", "build", "-o", BIN, "./cmd/chiron"], ["go", "test", "-c", "-o", MCP, "./internal/mcp"]):
        subprocess.run(args, cwd=ROOT, check=True)


def user_settings():
    """The model and server of the user's own config, without the key."""
    base, model = os.environ.get("CHIRON_LIVE_BASE_URL"), os.environ.get("CHIRON_LIVE_MODEL")
    if base is None or model is None:
        try:
            with open(os.path.expanduser("~/.config/chiron/config.json")) as f:
                cfg = json.load(f)
            base = cfg.get("base_url", "") if base is None else base
            model = cfg.get("model", "") if model is None else model
        except OSError:
            pass
    return base or "", model or ""


def write_config(name, servers, **extra):
    base, model = user_settings()
    cfg = {"base_url": base, "model": model, "mcpServers": servers, **extra}
    path = os.path.join(WORK, name + ".json")
    with open(path, "w") as f:
        json.dump(cfg, f)
    os.chmod(path, 0o600)
    return path


def local_server(**extra):
    env = {"CHIRON_TEST_MCP_MODE": "serve", "E2E_LINES": MANY_LINES}
    return {"command": MCP, "env": env, **extra}


def start(servers=None, args=(), rows=40, cols=120, config="default", cwd=None, env=None, **settings):
    path = write_config(config, servers if servers is not None else {"local": local_server()}, **settings)
    env = {"XDG_STATE_HOME": os.path.join(WORK, "state"), **(env or {})}
    return Term([BIN, "--config", path, *args], env=env, rows=rows, cols=cols, cwd=cwd)


def preflight():
    path = write_config("preflight", {})
    r = subprocess.run([BIN, "--config", path, "-p", "Reply with exactly: PONG"], capture_output=True, text=True, timeout=120,
                       env={**os.environ, "XDG_STATE_HOME": os.path.join(WORK, "state")})
    if "PONG" not in r.stdout:
        raise SystemExit(f"the model is not reachable (stdout {r.stdout!r}, stderr {r.stderr!r}): set CHIRON_LIVE_BASE_URL and CHIRON_LIVE_MODEL")


# --- helpers -------------------------------------------------------------------------------------------------

def invariants(t, where):
    """What must be true of every settled screen."""
    margin = [i for i, l in enumerate(t.screen.display) if len(l.rstrip()) > t.cols - 1]
    t.expect(not margin, f"{where}: line(s) {margin} touch the last column, which is the margin")
    for text in ("Working", "Cancelling", "rking…", "", "Welcome"):
        t.lacks(text, f"{where}: the screen shows {text!r}")
    t.expect("chiron" in t.lines[0], f"{where}: the header is gone")


def settle(t, where):
    t.idle()
    invariants(t, where)


def say(t, prompt, where=None):
    t.type(prompt)
    settle(t, where or prompt[:30])


def ask_for_tool(t, prompt, retry=None, settle=0.5):
    """Sends a prompt that should make the model call a tool, and waits for the approval dialog."""
    for p in (prompt, retry or prompt + " You must call the tool; do not answer in words first."):
        t.type(p)
        try:
            t.wait_for("Approval needed", 60)
            t.pump(settle)  # the frame may still be arriving in pieces
            return
        except Failure:
            settle(t, "no tool call")
    raise Skip("the model did not call the tool")


def answer(t, key):
    """Answers the dialog: keys right after it opens are ignored on purpose, so wait out the guard."""
    t.pump(0.7)
    t.send(key, 0.4)


def row_of(t, text):
    """The index of the first row holding text (not the model's prose about it: give a card's header)."""
    for y, line in enumerate(t.lines):
        if text in line:
            return y
    raise Failure(f"no row holds {text!r}\n{t.dump()}")


def row_bg(t, text):
    """The background colours of the cells of the first row holding text."""
    for y, line in enumerate(t.screen.display):
        if text in line:
            return [t.screen.buffer[y][x].bg for x in range(t.cols)]
    raise Failure(f"no row holds {text!r}\n{t.dump()}")


# --- scenarios -----------------------------------------------------------------------------------------------

def startup_and_chat():
    t = start()
    try:
        t.wait_for("mcp 1/1", 30)
        t.contains("connected · 5 tools")
        t.contains("beam:cloud") if "beam" in user_settings()[1] else None
        t.contains("Ask anything, or /help for commands...")
        t.lacks("Welcome")
        # the terminal's own cursor sits in the input, and the title names the program and the directory
        prompt = t.find("❯ ")
        t.expect(prompt is not None and not t.screen.cursor.hidden, "no cursor shown")
        t.expect((t.screen.cursor.x, t.screen.cursor.y) == (prompt[0] + 2, prompt[1]), f"the cursor is at {(t.screen.cursor.x, t.screen.cursor.y)}, not at the start of the input {(prompt[0] + 2, prompt[1])}")
        t.expect(t.screen.title.startswith("chiron"), f"window title {t.screen.title!r}")
        t.type("Write a 200 word essay about the sea.", enter=True)
        t.wait_for("Working", 20)
        t.expect(t.screen.title.startswith("●"), f"the title does not show the work: {t.screen.title!r}")
        t.idle(120)
        t.expect(not t.screen.title.startswith("●"), f"the title still shows work: {t.screen.title!r}")
        say(t, "Reply with exactly: PONG")
        t.expect("   PONG" in t.lines, "the answer is not drawn cleanly on its own row")
        t.expect(re.search(r"[\d.]+k? tokens · \d+%$", t.lines[-1].rstrip()), f"the bottom row does not end with the usage: {t.lines[-1]!r}")
        t.expect("tokens" not in t.lines[0] and "context" not in t.lines[-1], "the header shows the usage, or the bottom row says context")
        return t
    finally:
        t.close()


def markdown():
    t = start()
    try:
        t.wait_for("mcp 1/1", 30)
        t.type("Answer in markdown, briefly: a heading, one sentence with **bold** and `inline code`, a list of two items, "
               "and a fenced bash code block containing exactly one line: ls -la")
        seen_raw = None
        end = time.time() + 120
        while time.time() < end:
            t.pump(0.3)
            for marker in ("**", "```"):
                if marker in t.text and "Answer in markdown" not in t.text.split(marker)[0][-200:]:
                    # the user's own message holds markers; look only below it
                    body = t.text.split("ls -la", 1)[-1] if "ls -la" in t.text else ""
                    if marker in body:
                        seen_raw = marker
            if "Working" not in t.text and "tokens" in t.lines[-1] and t.text.count("ls -la") >= 2:
                break
        t.idle()
        invariants(t, "markdown")
        t.expect(seen_raw is None, f"raw markdown {seen_raw!r} was on screen while the reply streamed or after it")
        lines = [l for l in t.lines if "ls -la" in l]
        t.expect(len(lines) >= 2, "the code block did not appear")
        bg = row_bg(t, "ls -la") if len(lines) >= 2 else None
        # the code line is the last row holding the text; its cells have a background across the band
        code_y = max(y for y, l in enumerate(t.screen.display) if "ls -la" in l)
        shaded = [t.screen.buffer[code_y][x].bg for x in range(4, t.cols - 4)]
        t.expect(all(b != "default" for b in shaded), "the code line is not on a shaded band")
        return t
    finally:
        t.close()


def approval_and_cards():
    t = start()
    try:
        t.wait_for("mcp 1/1", 30)
        # a key typed right after the dialog opened must not answer it, so none of the settling time is spent first
        ask_for_tool(t, "Call the local__echo tool with the text hello. Call it exactly once.", settle=0)
        t.send("y", 0.3)
        t.contains("Approval needed", "a key right after the dialog opened answered it")
        for text in ("local → echo", "text: hello", "Runs: ", "Allow", "Deny", "For the session"):
            t.contains(text, f"the dialog is missing {text!r}")
        t.expect("\\u0026" not in t.text, "the dialog shows a JSON escape")
        t.pump(0.7)  # out of the guard time
        col, row = t.find("Deny")
        t.click(col + 1, row)  # a click on the button answers like the key
        settle(t, "after deny")
        t.contains("⊘", "the denied call has no denied card")
        t.contains("denied by you")

        # allow for the session: the second call asks, the third does not
        ask_for_tool(t, "Call local__echo again with the text second. Call it exactly once.")
        answer(t, "s")
        settle(t, "after allow for session")
        t.contains("1 line", "the result card does not say how much it returned")
        y = row_of(t, "✓ local → echo (text=second)")
        t.expect(t.lines[y + 1].strip() == "", "a closed card shows its output under its header")
        t.type("Call local__echo once more with the text third. Call it exactly once.")
        t.wait_for("echo", 5)
        t.idle()
        invariants(t, "third call")
        t.lacks("Approval needed", "the session grant did not cover the third call")
        t.type("/tools")
        t.wait_for("allowed this session", 10)
        t.contains("local__echo")

        # open and close a card
        t.send(TAB, 0.4)
        t.contains("▸", "Tab did not select a card")
        t.send(ENTER, 0.4)
        y = row_of(t, "local → echo (text=third)")
        t.expect("echo: third" in t.lines[y + 1], "opening the card did not show its output under it")
        t.send(ENTER, 0.4)
        y = row_of(t, "local → echo (text=third)")
        t.expect("echo: third" not in t.lines[y + 1], "closing the card did not hide its output")
        t.send(ESC, 0.4)
        t.lacks("▸", "Esc did not end the selection")
        return t
    finally:
        t.close()


def queue_and_cancel():
    t = start()
    try:
        t.wait_for("mcp 1/1", 30)
        t.type("Write a 250 word essay about the sea.")
        t.wait_for("Working", 20)
        t.type("Reply with exactly: QUEUED")
        t.contains("› queued", "the prompt typed during a turn was not queued")
        t.idle(180)
        invariants(t, "after the queue ran")
        t.contains("QUEUED", "the queued prompt never ran")
        t.lacks("› queued")

        # Ctrl-C cancels and drops what is queued
        t.type("Write a very long story, at least 400 words.")
        t.wait_for("Working", 20)
        t.type("first queued")
        t.type("second queued")
        t.send(CTRL_C, 0.5)
        t.wait_for("Cancelled", 30)
        t.contains("Dropped 2 queued prompts")
        t.pump(3)
        t.lacks("Working", "a queued prompt ran after the cancel")
        t.lacks("› queued")
        invariants(t, "after cancel")
        return t
    finally:
        t.close()


def commands():
    t = start()
    try:
        t.wait_for("mcp 1/1", 30)
        t.type("/help")
        for text in ("/clear", "/context", "/model", "/servers", "/undo", "mouse wheel", "ctrl+c"):
            t.contains(text, f"/help does not list {text!r}")
        t.type("/nosuch")
        t.contains("Unknown command: /nosuch. Type /help to list commands.")
        t.type("/servers")
        t.contains("connected · 5 tools")
        t.contains("local.log")
        t.type("/tools")
        t.contains("ask first")
        t.type("/context")
        for text in ("instructions", "history", "total"):
            t.contains(text)
        t.type("/model")
        t.contains("Model: ")
        t.type("/model other-model")
        t.contains("Model set to other-model")
        t.contains("other-model", "the header does not show the new model")
        t.type("/model " + (user_settings()[1] or "gpt-5.4-mini"))
        t.type("/effort")
        t.contains("Reasoning effort: model default")
        t.type("/effort bogus")
        t.contains("Command error:")
        t.type("/undo")
        t.contains("Nothing to undo")
        t.type("/retry")
        t.contains("Nothing to retry")

        # suggestions
        t.send("/re", 0.4)
        for text in ("/reconnect", "/reset", "/retry"):
            t.contains(text, "the suggestion list is missing " + text)
        t.send(ESC, 0.3)
        t.lacks("/reconnect [NAME]")
        t.send("\x15", 0.3)  # Ctrl-U: clear the line (the input's own binding)

        # a command during a turn is refused
        t.type("Write a 300 word essay about rivers.")  # slow enough to type during
        t.wait_for("Working", 20)
        t.type("/help")
        t.contains("cannot run during a turn")
        t.send(CTRL_C, 0.5)
        t.wait_for("Cancelled", 30)
        t.send("\x15", 0.3)
        settle(t, "after cancel")

        # a turn, /undo, /retry, /clear, /reset
        say(t, "Reply with exactly: ONE")
        t.type("/undo")
        t.contains("Last exchange removed")
        t.type("/retry")
        t.wait_for("Working", 20)
        t.idle()
        t.type("/clear")
        t.pump(0.5)
        t.lacks("Reply with exactly: ONE", "/clear left the transcript on screen")
        t.lacks("Welcome")
        t.send(CTRL_C, 0.4)
        t.contains("again to quit", "/clear forgot that a conversation exists")
        t.send("x", 0.3)
        t.send("\x15", 0.3)
        t.type("/reset")
        t.contains("Conversation reset")
        t.send(CTRL_C, 0.4)
        t.expect(t.exited() == 0, "after /reset there is nothing to lose, so Ctrl-C should quit at once")
        return t
    finally:
        t.close()


def restart_after_death():
    t = start(servers={"local": local_server(autoApprove=True)})
    try:
        t.wait_for("mcp 1/1", 30)
        t.type("Call the local__die tool now with no arguments. Call it exactly once.")
        t.wait_for("lost", 60)
        t.idle()
        invariants(t, "after the death")
        t.type("/servers")
        t.contains("connection lost")
        say(t, "Call the local__echo tool with the text back. Call it exactly once.")
        t.contains("1 line", "the call after the death did not run")
        t.type("/servers")
        t.contains("started again 1 time")
        t.type("/reconnect")
        t.wait_for("Nothing to reconnect", 20)
        return t
    finally:
        t.close()


def scrolling():
    t = start(servers={"local": local_server(autoApprove=True)}, rows=20)
    try:
        t.wait_for("mcp 1/1", 30)
        say(t, "Call the local__env tool with the name E2E_LINES, once, and then say DONE.")
        t.send(TAB, 0.3)
        t.send(ENTER, 0.5)  # open the card: 40 lines in a window of 20 rows
        t.send(ESC, 0.3)
        t.send(PGDN, 0.3)
        t.send(PGDN, 0.3)
        t.send(PGDN, 0.3)
        t.lacks("below", "the hint shows at the bottom")
        t.send(PGUP, 0.4)
        t.contains("below", "Page Up did not scroll (no hint)")
        t.send(UP, 0.4)
        t.send(DOWN, 0.4)
        t.send(PGUP, 0.4)
        before = re.search(r"↓ (\d+) lines? below", t.text)
        t.expect(before, "the hint has no count")
        t.send(WHEEL_UP, 0.4)
        after = re.search(r"↓ (\d+) lines? below", t.text)
        t.expect(int(after.group(1)) > int(before.group(1)), "the wheel did not scroll up")
        t.send(WHEEL_DOWN, 0.4)
        for _ in range(12):
            t.send(PGDN, 0.2)
        t.lacks("below", "scrolling back down did not reach the bottom")
        t.expect("chiron" in t.lines[0], "the header is gone")
        # typing is not scrolling
        t.send("abc", 0.3)
        t.contains("abc")
        return t
    finally:
        t.close()


def resize():
    t = start()
    try:
        t.wait_for("mcp 1/1", 30)
        say(t, "Reply with exactly: RESIZE")
        for rows, cols in ((20, 60), (12, 40), (16, 45), (50, 140), (30, 100)):
            t.resize(rows, cols)
            t.pump(0.6)
            t.expect(t.exit_code is None, f"the program died at {cols}x{rows}")
            t.expect("chiron" in t.lines[0], f"no header at {cols}x{rows}")
            t.expect("/help" in t.text, f"no hint in the input at {cols}x{rows}")
            wide = [i for i, l in enumerate(t.screen.display) if len(l.rstrip()) > cols - 1]
            t.expect(not wide, f"{cols}x{rows}: lines {wide} touch the last column")
        # a resize while the dialog is open
        ask_for_tool(t, "Call the local__echo tool with the text resize. Call it exactly once.")
        t.resize(24, 70)
        t.contains("Approval needed")
        t.expect(len(t.lines) == 24, "the screen is not the size of the window")
        answer(t, "n")
        settle(t, "after the resized dialog")
        return t
    finally:
        t.close()


def quit_protection():
    t = start(servers={})
    try:
        t.wait_for("/help", 10)  # until the UI runs, the terminal turns Ctrl-C into a signal
        t.send(CTRL_C, 0.3)
        t.expect(t.exited() == 0, "Ctrl-C with no conversation should quit at once")
    finally:
        t.close()
    t = start(servers={})
    try:
        t.wait_for("/help", 10)
        say(t, "Reply with exactly: HI")
        t.send(CTRL_C, 0.4)
        t.contains("again to quit", "no warning on the first Ctrl-C")
        t.send("z", 0.3)
        t.lacks("again to quit", "another key did not take the warning back")
        t.send("\x15", 0.2)
        t.send(CTRL_C, 0.3)
        t.send(CTRL_C, 0.8)
        t.expect(t.exited() == 0, f"two Ctrl-C did not quit (exit {t.exit_code})")
    finally:
        t.close()
    t = start(servers={})
    try:
        t.wait_for("/help", 10)
        say(t, "Reply with exactly: HI")
        t.send(CTRL_D, 0.4)
        t.contains("again to quit")
        t.send(CTRL_D, 0.8)
        t.expect(t.exited() == 0, "two Ctrl-D did not quit")
    finally:
        t.close()
    return t


def multiline_input():
    t = start(servers={})
    try:
        t.send("first line", 0.2)
        t.send(ALT_ENTER, 0.3)
        t.send("second line", 0.3)
        t.contains("first line")
        t.contains("second line")
        t.send(ENTER, 0.4)
        t.wait_for("┃", 10)
        t.idle()
        t.contains("first line")
        t.contains("second line")
        return t
    finally:
        t.close()


def long_output_card():
    t = start(servers={"local": local_server(autoApprove=True)})
    try:
        t.wait_for("mcp 1/1", 30)
        say(t, "Call the local__env tool with the name E2E_LINES, once, and then say DONE.")
        t.contains("40 lines", "the closed card does not count the lines")
        t.lacks("row 7", "a closed card shows its output")
        # a click opens the card, and another closes it
        col, row = t.find("✓ local → env")
        t.click(col + 5, row)
        t.contains("row 7", "a click on the card did not open it")
        col, row = t.find("local → env")
        t.click(col + 5, row)
        t.lacks("row 7", "a second click did not close it")
        t.send(TAB, 0.4)
        t.send(ENTER, 0.5)
        t.expect(any(l.strip() == "row 1" for l in t.lines), "the opened card does not show its output from the top")
        for _ in range(3):
            t.send(PGDN, 0.3)
        t.contains("row 40", "the opened card does not hold the whole output")
        t.send(ENTER, 0.4)
        t.lacks("row 40")
        return t
    finally:
        t.close()


def failing_server_and_flags():
    t = start(servers={"good": local_server(), "bad": {"command": MCP, "env": {"X": "${E2E_SURELY_UNSET}"}}})
    try:
        t.wait_for("mcp 1/2", 30)
        t.contains("failed: ")
        t.contains("${E2E_SURELY_UNSET}", "the error does not name the variable")
        t.type("/servers")
        t.contains("good")
        return t
    finally:
        t.close()
    # --no-mcp is checked below, with -p


def non_interactive():
    path = write_config("print", {"local": local_server(autoApprove=True)})
    env = {**os.environ, "XDG_STATE_HOME": os.path.join(WORK, "state")}
    prompt = "Call the local__echo tool with the text ping and tell me exactly what it returned."
    r = subprocess.run([BIN, "--config", path, "-p", prompt], capture_output=True, text=True, timeout=180, env=env)
    if "echo: ping" not in r.stdout:
        raise Skip(f"the model did not call the tool (stdout {r.stdout!r})")
    # without autoApprove a call is denied in -p mode, with --yes it runs
    path2 = write_config("print2", {"local": local_server()})
    denied = subprocess.run([BIN, "--config", path2, "-p", prompt], capture_output=True, text=True, timeout=180, env=env)
    if "denied" not in denied.stderr:
        raise Failure(f"-p without --yes should deny the call: stderr {denied.stderr!r}, stdout {denied.stdout!r}")
    allowed = subprocess.run([BIN, "--config", path2, "-p", prompt, "--yes"], capture_output=True, text=True, timeout=180, env=env)
    if "echo: ping" not in allowed.stdout or "denied" in allowed.stderr:
        raise Failure(f"-p --yes should run the call: stdout {allowed.stdout!r}, stderr {allowed.stderr!r}")
    off = subprocess.run([BIN, "--config", path, "-p", prompt, "--no-mcp"], capture_output=True, text=True, timeout=180, env=env)
    if "echo: ping" in off.stdout:
        raise Failure("--no-mcp still ran an MCP tool")


# --- the real toolbox server of the user's config ---------------------------------------------------------------

def toolbox_entry():
    """The user's own `toolbox` MCP server, as configured (its secrets stay in the environment, never read here)."""
    try:
        with open(os.path.expanduser("~/.config/chiron/config.json")) as f:
            return json.load(f)["mcpServers"]["toolbox"]
    except (OSError, KeyError):
        return None


def toolbox_term(**kw):
    entry = toolbox_entry()
    if entry is None:
        raise Skip("no toolbox server in the config file")
    entry = {**entry, "enabled": True}
    scratch = tempfile.mkdtemp(prefix="tb-", dir=WORK)
    t = start(servers={"toolbox": entry}, cwd=scratch, config="toolbox", **kw)
    t.scratch = scratch
    try:
        t.wait_for(re.compile(r"mcp \d/1"), 90)
    except Failure:
        t.close()
        raise
    if "mcp 1/1" not in t.text:  # too slow for the startup deadline (npx): the way in is /reconnect
        t.contains("did not start within", "the toolbox failed for a reason other than being slow")
        t.type("/reconnect toolbox")
        t.wait_for("mcp 1/1", 90, "the toolbox to join after /reconnect")
    return t


def toolbox_startup():
    t = toolbox_term()
    try:
        t.type("/servers")
        t.contains("connected · 7 tools", "the toolbox does not offer the 7 tools it should")
        t.type("/tools")
        t.wait_for("ask first", 10)
        flat = " ".join(t.text.split())
        for name in ("toolbox__edit_file", "toolbox__write_file", "toolbox__shell"):
            t.expect(name in flat.split("ask first")[-1], f"{name} should ask first")
        t.expect("toolbox__read_file" in flat.split("run without asking")[-1].split("ask first")[0], "read_file should run without asking")
    finally:
        t.close()


def toolbox_files():
    t = toolbox_term()
    try:
        path = os.path.join(t.scratch, "hello.txt")
        ask_for_tool(t, "Use the toolbox__write_file tool to create the file hello.txt containing exactly two lines: "
                        "'first line' and 'second line'. Call it exactly once.")
        for text in ("toolbox → write_file", "Runs: npx", "content:"):
            t.contains(text, f"the dialog is missing {text!r}")
        t.expect("\\n" not in t.text.split("Approval needed")[-1], "the dialog shows an escaped newline instead of the lines")
        answer(t, "y")
        settle(t, "after write_file")
        t.expect(os.path.exists(path), "the file was not written")
        t.expect(open(path).read().strip().splitlines() == ["first line", "second line"], f"wrong content: {open(path).read()!r}")

        # a read-style tool runs without asking
        t.type("Use toolbox__read_file to read hello.txt and tell me its second line.")
        t.wait_for("read_file", 60)
        t.idle()
        invariants(t, "after read_file")
        t.lacks("Approval needed", "read_file asked although the config trusts it")
        t.contains("toolbox → read_file")
        t.contains("second line")

        ask_for_tool(t, "Use toolbox__edit_file to replace the text 'second line' with 'SECOND LINE' in hello.txt. Call it exactly once.")
        t.contains("toolbox → edit_file")
        answer(t, "y")
        settle(t, "after edit_file")
        t.expect("SECOND LINE" in open(path).read(), f"the edit did not reach the disk: {open(path).read()!r}")

        with open(os.path.join(t.scratch, "other.txt"), "w") as f:
            f.write("other file\n")
        t.type("Use toolbox__read_multiple_files to read hello.txt and other.txt, then say DONE.")
        t.wait_for("read_multiple_files", 60)
        t.idle()
        invariants(t, "after read_multiple_files")
        t.lacks("Approval needed")
    finally:
        t.close()


def toolbox_shell():
    t = toolbox_term()
    try:
        ask_for_tool(t, "Use the toolbox__shell tool to run exactly this command: echo hello-from-shell && pwd")
        t.contains("toolbox → shell")
        t.contains("command: echo hello-from-shell && pwd", "the dialog does not show the command decoded, on one line")
        t.expect("\\u0026" not in t.text, "the dialog shows a JSON escape")
        # denying tells the model, which must not retry
        answer(t, "n")
        settle(t, "after deny")
        t.contains("denied by you")
        calls = t.text.count("toolbox → shell")
        t.expect(calls == 1, f"the denied call was retried ({calls} cards)")

        ask_for_tool(t, "Run the same shell command again with toolbox__shell: echo hello-from-shell && pwd")
        answer(t, "y")
        settle(t, "after shell")
        y = row_of(t, "toolbox → shell")
        y = max(i for i, l in enumerate(t.lines) if "toolbox → shell" in l and "⊘" not in l)
        t.expect(re.search(r"\d+ lines?", t.lines[y]), "the shell card does not say how much it returned")
        t.send(TAB, 0.3)
        t.send(TAB, 0.3) if "▸" not in t.text else None
        t.send(ENTER, 0.5)
        t.contains("hello-from-shell", "the opened card does not show the command's output")
    finally:
        t.close()


def toolbox_fetch_and_search():
    t = toolbox_term()
    try:
        t.type("Use toolbox__fetch_url to fetch https://example.com and tell me the page's heading.")
        t.wait_for("fetch_url", 90)
        t.idle(180)
        invariants(t, "after fetch_url")
        t.lacks("Approval needed", "fetch_url asked although the config trusts it")
        t.contains("toolbox → fetch_url")
        t.expect(re.search(r"\d+ lines?", t.lines[row_of(t, "✓ toolbox → fetch_url")]), "the fetch card has no line count")
        t.contains("Example Domain")

        t.type("Use toolbox__brave_web_search to search for 'Go programming language' once, and name one result.")
        t.wait_for("brave_web_search", 90)
        t.idle(180)
        invariants(t, "after brave_web_search")
        t.contains("toolbox → brave_web_search")
        if "ERROR" in t.text.split("brave_web_search")[-1][:400]:
            raise Skip("the search failed (key, quota or network): " + " ".join(t.text.split("brave_web_search")[-1][:300].split()))
    finally:
        t.close()


def toolbox_large_output():
    t = toolbox_term()
    try:
        plan = os.path.join(ROOT, "PLAN.md")
        t.type(f"Use toolbox__read_file to read {plan} and tell me its first heading.")
        t.wait_for("read_file", 60)
        t.idle(180)
        invariants(t, "after a large read")
        t.contains("toolbox → read_file")
        t.send(TAB, 0.3)
        t.send(ENTER, 0.6)
        for _ in range(60):  # to the end of the opened card
            t.send(PGDN, 0.05)
        t.pump(0.5)
        t.contains("[output truncated", "a result over the cap is not cut with a note")
        t.send(ENTER, 0.4)
    finally:
        t.close()


def clipboard_and_title():
    t = start(servers={})
    try:
        t.wait_for("/help", 10)
        t.type("/copy")
        t.contains("Nothing to copy")
        say(t, "Reply with exactly: COPYME")
        t.type("/copy")
        t.contains("Copied the last answer")
        t.pump(0.3)
        import base64
        payloads = re.findall(rb"\x1b\]52;c;([A-Za-z0-9+/=]*)(?:\x07|\x1b\\)", t.raw)
        t.expect(payloads, "no OSC 52 sequence reached the terminal")
        t.expect(b"COPYME" in base64.b64decode(payloads[-1]), f"the clipboard holds {base64.b64decode(payloads[-1])!r}")
        # the help names the new-line key that works here: this emulator never says it supports Shift+Enter
        t.contains("ctrl+j new line")
        return t
    finally:
        t.close()


def compact():
    t = start(servers={}, config="compact", max_context_tokens=900)
    try:
        t.wait_for("/help", 10)
        t.type("/compact")
        t.contains("Nothing to summarize yet")
        t.type("/summary")
        t.contains("No summary yet")
        filler = " Background that matters for nothing later: " + "the weather was mild and the meeting ran long. " * 8
        for fact in ("The code word is PINEAPPLE-42.", "The service listens on port 7731.", "Never use the word synergy.",
                     "The database is Postgres 16."):
            say(t, fact + filler + " Reply with just OK.", fact[:20])
        t.type("/compact keep every number")
        t.wait_for("Summarized", 120)
        t.idle()
        invariants(t, "after /compact")
        t.type("/summary")
        t.contains("## Goal")
        t.type("/context")
        t.contains("replaces the oldest turns")
        t.contains("budget")
        t.type("What was the code word, and which port does the service listen on? Answer briefly.")
        t.wait_for("PINEAPPLE-42", 90)
        t.idle()
        t.contains("7731", "the summary lost a fact from the replaced turns")
        invariants(t, "question after the summary")
        t.type("/undo")
        t.contains("Last exchange removed")
        t.type("/retry")
        t.wait_for("PINEAPPLE-42", 90)
        t.idle()
        t.type("/reset")
        t.type("/summary")
        t.contains("No summary yet")
        return t
    finally:
        t.close()


def prompt_history():
    editor = os.path.join(WORK, "fake-editor.sh")
    with open(editor, "w") as f:
        f.write("#!/bin/sh\nprintf 'edited in the editor' > \"$1\"\n")
    os.chmod(editor, 0o755)
    t = start(servers={}, config="history", env={"EDITOR": editor, "VISUAL": ""})
    try:
        t.wait_for("/help", 10)
        say(t, "Reply with exactly: ALPHA-7")
        say(t, "Reply with exactly: BETA-8")
        t.send(UP, 0.4)
        t.contains("❯ Reply with exactly: BETA-8", "Up did not recall the last prompt")
        t.send(UP, 0.4)
        t.contains("❯ Reply with exactly: ALPHA-7", "a second Up did not recall the one before")
        t.send(DOWN, 0.4)
        t.contains("❯ Reply with exactly: BETA-8")
        t.send(DOWN, 0.4)
        t.lacks("❯ Reply with exactly", "Down past the newest should empty the input")
        t.send("draft", 0.3)
        t.send(UP, 0.3)
        t.lacks("❯ Reply with exactly", "Up replaced typed text")
        t.send("\x15", 0.3)  # Ctrl-U

        t.send("\x12", 0.3)  # Ctrl-R
        t.send("ALPHA", 0.4)
        t.contains("search ALPHA", "no search row")
        t.contains("older match", "the help bar does not describe the search")
        t.send(ENTER, 0.4)
        t.contains("❯ Reply with exactly: ALPHA-7", "Enter did not put the match in the input")
        t.lacks("search ALPHA")
        t.send("\x15", 0.3)

        t.send("my draft", 0.3)
        t.send("\x07", 1.5)  # Ctrl-G: the editor, which replaces the text
        t.wait_for("edited in the editor", 10)
        t.lacks("my draft")
        t.pump(0.5)
        t.expect(t.text.count("edited in the editor") == 1, "the editor's text was sent, not put in the input")
        invariants(t, "after the editor")
        t.send("\x15", 0.3)
        t.send(CTRL_C, 0.3)
        t.send(CTRL_C, 0.8)
        t.expect(t.exited() == 0, "two Ctrl-C did not quit")
    finally:
        t.close()

    path = os.path.join(WORK, "state", "chiron", "history")
    t.expect(os.stat(path).st_mode & 0o777 == 0o600, "the history file is not private")
    t = start(servers={}, config="history")
    try:
        t.wait_for("/help", 10)
        t.send(UP, 0.4)
        t.contains("❯ Reply with exactly: BETA-8", "the history did not survive a restart")
        t.send("\x15", 0.3)
        return t
    finally:
        t.close()


def sessions():
    work = os.path.join(WORK, "sessions-dir")  # its own directory: a bare --resume picks the latest session of the directory
    os.makedirs(work, exist_ok=True)
    t = start(servers={}, config="sessions", cwd=work)
    try:
        t.wait_for("/help", 10)
        t.type("/sessions")
        t.contains("No saved sessions yet")
        say(t, "The code word is PINEAPPLE-42. Reply with just OK.", "the fact")
        t.type("/sessions")
        t.contains("Resume a session of this directory")
        t.contains("(this one)")
        t.contains("1 turn")
        t.send(ESC, 0.4)
        t.send(CTRL_C, 0.3)
        t.send(CTRL_C, 0.8)
        t.expect(t.exited() == 0, "two Ctrl-C did not quit")
    finally:
        t.close()

    t = start(servers={}, config="sessions", cwd=work, args=["--resume"])
    try:
        t.wait_for("Resumed session", 10)
        t.contains("PINEAPPLE-42", "the earlier prompt is not shown again")
        invariants(t, "after resume")
        t.type("What was the code word? Answer with just the word.")
        t.wait_for("PINEAPPLE-42", 60)
        t.idle()
        t.type("/sessions")
        t.contains("2 turns")
        t.send(ESC, 0.4)
        t.type("/undo")
        t.type("/retry")
        t.wait_for("Working", 20)
        t.idle()
        invariants(t, "retry after resume")
    finally:
        t.close()

    # A fresh conversation in the same directory picks the saved session from the list: no model call is needed.
    t = start(servers={}, config="sessions", cwd=work)
    try:
        t.wait_for("/help", 10)
        t.type("/sessions")
        t.contains("Resume a session of this directory")
        t.send(ENTER, 0.6)
        t.contains("Resumed session")
        t.contains("PINEAPPLE-42", "the saved conversation is shown after choosing it")
        invariants(t, "after choosing a session")
    finally:
        t.close()

    # Deleting needs a confirmation, and the last session deleted closes the list. The session in use cannot be deleted,
    # so this conversation is a fresh one that was never saved.
    t = start(servers={}, config="sessions", cwd=work)
    try:
        t.wait_for("/help", 10)
        t.type("/sessions")
        t.contains("Resume a session of this directory")
        t.send("d", 0.4)
        t.contains("Delete session")
        t.send("n", 0.4)
        t.contains("Resume a session of this directory", "n keeps the session")
        t.send("d", 0.4)
        t.send("y", 0.6)
        t.contains("Deleted session", "y deletes it")
        t.type("/sessions")
        t.contains("No saved sessions yet")
        invariants(t, "after deleting a session")
        return t
    finally:
        t.close()


def todo():
    """The model keeps a todo list with the todo tool: it never asks, the panel shows the open steps, /todo prints them."""
    t = start()
    try:
        t.wait_for("mcp 1/1", 30)
        for p in ("Use the todo tool once to write a plan for painting a fence: exactly three steps, the first in_progress and the other two pending. Then reply with just OK.",
                  "You must call the todo tool with three steps, the first in_progress. Do not answer in words first."):
            t.type(p)
            try:
                t.wait_for("todo · ", 60)
                break
            except Failure:
                settle(t, "no todo call")
        else:
            raise Skip("the model did not call the todo tool")
        t.idle()
        t.lacks("Approval needed", "the todo tool asked for approval")
        t.contains("▸", "the step in progress is not marked")
        t.contains("○", "a pending step is not marked")
        t.expect(re.search(r"todo · 0/3 done", t.text), "the panel does not count the steps")
        invariants(t, "todo panel")
        t.type("/todo")
        t.contains("[>]")
        t.contains("[ ]")
        t.type("Now call the todo tool once more with the same three steps all marked completed. Then reply with just OK.")
        t.wait_for("Working", 20)
        t.idle()
        t.expect("todo · " not in t.text, "the panel stays after every step is completed")
        t.type("/todo")
        t.contains("[x]", "the finished list is not printed")
        t.type("/reset")
        t.type("/todo")
        t.contains("No todos", "/reset did not clear the list")
        return t
    finally:
        t.close()


SCENARIOS = [startup_and_chat, clipboard_and_title, markdown, approval_and_cards, queue_and_cancel, commands, restart_after_death, scrolling,
             resize, quit_protection, multiline_input, long_output_card, failing_server_and_flags, non_interactive,
             compact, sessions, prompt_history, todo, toolbox_startup, toolbox_files, toolbox_shell, toolbox_fetch_and_search, toolbox_large_output]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-k", help="run only scenarios whose name contains this")
    ap.add_argument("--keep", action="store_true", help="keep the temporary directory")
    args = ap.parse_args()
    build()
    preflight()
    results = []
    for scenario in SCENARIOS:
        if args.k and args.k not in scenario.__name__:
            continue
        started = time.time()
        try:
            scenario()
            status, detail = "PASS", ""
        except Skip as e:
            status, detail = "SKIP", str(e)
        except Failure as e:
            status, detail = "FAIL", str(e)
        except Exception:  # a bug in the scenario or the harness
            status, detail = "ERROR", traceback.format_exc()
        results.append((scenario.__name__, status))
        print(f"[{status}] {scenario.__name__} ({time.time() - started:.0f}s)", flush=True)
        if detail:
            print(detail, flush=True)
    print("\n" + "\n".join(f"{s:5} {n}" for n, s in results))
    print(f"(work dir {WORK})")
    sys.exit(1 if any(s in ("FAIL", "ERROR") for _, s in results) else 0)


if __name__ == "__main__":
    main()
