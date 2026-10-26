"""Runs cells in Jupyter kernels for the daemon.

Reads one JSON request per line on stdin and writes one JSON reply per line
on stdout. Only replies go to stdout: the kernels' own output goes to
/dev/null and anything this process would print goes to stderr.
"""

import base64
import json
import os
import queue
import re
import signal
import subprocess
import sys
import time

from jupyter_client import KernelManager
from jupyter_client.kernelspec import KernelSpecManager

TIME_LIMIT = "the cell ran past its time limit; the session restarted and its variables are gone"
STOPPED = "the session's kernel stopped; it restarted and its variables are gone"
START_TIMEOUT = 180
ANSI = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]")
# Each of stdout and error keeps at most CAP bytes of its JSON encoding, so a
# reply always fits the line the daemon reads.
CAP = 32 << 20
CUT = "\n[output cut at 32 MiB]\n"

replies = sys.stdout
sys.stdout = sys.stderr
cwd = os.getcwd()

# Kernels come only from the bases' kernelspec folders on JUPYTER_PATH, never
# from the person's own Jupyter setup.
_dirs = [os.path.join(p, "kernels") for p in os.environ.get("JUPYTER_PATH", "").split(os.pathsep) if p]
specs = KernelSpecManager(kernel_dirs=_dirs)
named_specs = KernelSpecManager(kernel_dirs=_dirs, ensure_native_kernel=False)

sessions = {}
named_session = None
active_kernel = None
cell_interrupted = False
pending_interrupt = False
CELL_CAP = 20 << 20
CELL_CUT = {"type": "text", "text": "(output cut at 20 MiB)"}



def kernel_name(language):
    if not _dirs:
        raise RuntimeError("no kernels are installed for this study")
    if language == "python":
        return "python3"
    if language == "r":
        return "ir"
    if language == "julia":
        # IJulia names its kernelspec after the Julia version, julia-<major.minor>.
        names = [n for n in specs.find_kernel_specs() if n.startswith("julia-")]
        if names:
            return max(names, key=lambda n: [int(d) for d in re.findall(r"\d+", n)])
        raise RuntimeError("no Julia kernel is installed")
    raise RuntimeError("unknown language %r" % language)


def start(language):
    km = KernelManager(kernel_name=kernel_name(language), kernel_spec_manager=specs)
    if language == "python":
        # The python3 spec names the base's interpreter; the study's venv, which
        # runs this bridge, sees the base's packages and its own.
        km.kernel_spec.argv[0] = sys.executable
    km.start_kernel(cwd=cwd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    kc = km.client()
    try:
        kc.start_channels()
        kc.wait_for_ready(timeout=START_TIMEOUT)
    except Exception:
        stop(km, kc)
        raise
    return km, kc


def stop(km, kc):
    try:
        kc.stop_channels()
    except Exception:
        pass
    try:
        km.shutdown_kernel(now=True)
    except Exception:
        pass


def restart(language):
    # The session stays listed until the restart fails, so a shutdown that
    # interrupts the restart still stops its kernel.
    km, kc = sessions[language]
    try:
        km.restart_kernel(now=True)
        kc.wait_for_ready(timeout=START_TIMEOUT)
    except Exception:
        del sessions[language]
        stop(km, kc)


def run(language, code, timeout):
    if language not in sessions:
        sessions[language] = start(language)
    km, kc = sessions[language]
    msg_id = kc.execute(code, allow_stdin=False)
    out, err, failed = Capped(), Capped(), False
    deadline = time.monotonic() + timeout
    while True:
        if time.monotonic() > deadline:
            try:
                km.interrupt_kernel()
            except Exception:
                pass
            restart(language)
            return out.text(), TIME_LIMIT, False, True
        try:
            msg = kc.get_iopub_msg(timeout=0.2)
        except queue.Empty:
            if not km.is_alive():
                restart(language)
                return out.text(), STOPPED, False, True
            continue
        if msg["parent_header"].get("msg_id") != msg_id:
            continue
        kind, content = msg["msg_type"], msg["content"]
        if kind == "stream":
            (err if content.get("name") == "stderr" else out).add(content.get("text", ""))
        elif kind in ("execute_result", "display_data"):
            text = content.get("data", {}).get("text/plain")
            if text is not None:
                out.add(text if text.endswith("\n") else text + "\n")
        elif kind == "error":
            failed = True
            err.add("%s: %s\n" % (content.get("ename", ""), content.get("evalue", "")))
            for line in content.get("traceback", []):
                err.add(line + "\n")
        elif kind == "status" and content.get("execution_state") == "idle":
            break
    ok = reply_status(kc, msg_id) == "ok" and not failed
    return out.text(), err.text(), ok, False


def encoded(text):
    return len(json.dumps(text)) - 2


class Capped:
    """Collects text up to CAP bytes of JSON encoding; past that it drops what
    comes and notes the cut."""

    def __init__(self):
        self.parts, self.size, self.cut = [], 0, False

    def add(self, text):
        if self.cut:
            return
        n = encoded(text)
        if self.size + n > CAP:
            # The longest prefix whose encoding fits what is left.
            lo, hi = 0, len(text)
            while lo < hi:
                mid = (lo + hi + 1) // 2
                if encoded(text[:mid]) <= CAP - self.size:
                    lo = mid
                else:
                    hi = mid - 1
            text, n, self.cut = text[:lo], encoded(text[:lo]), True
        self.parts.append(text)
        self.size += n

    def text(self):
        s = ANSI.sub("", "".join(self.parts))
        return s + CUT if self.cut else s


def reply_status(kc, msg_id):
    # The shell reply follows within moments of idle; its status says whether
    # the cell failed even when the kernel reported nothing on iopub.
    end = time.monotonic() + 5
    while time.monotonic() < end:
        try:
            msg = kc.get_shell_msg(timeout=max(0.0, end - time.monotonic()))
        except queue.Empty:
            break
        if msg["parent_header"].get("msg_id") == msg_id:
            return msg["content"].get("status", "ok")
    return "ok"


def cell_json_size(value):
    # ASCII encoding overestimates Go's UTF-8 encoding, except for HTML
    # characters, which encoding/json escapes even in ordinary strings.
    text = json.dumps(value, ensure_ascii=True, separators=(",", ":"))
    return len(text) + 5 * sum(text.count(c) for c in "<>&")


class CellOutputs:
    def __init__(self):
        self.outputs, self.size, self.cut = [], 2, False

    def add(self, output):
        if self.cut:
            return
        # Reserve the final note and its separator before accepting output.
        budget = CELL_CAP - cell_json_size(CELL_CUT) - 1
        sep = int(bool(self.outputs))
        size = cell_json_size(output)
        if self.size + sep + size > budget:
            self.cut = True
            if output["type"] in ("stream", "text"):
                text = output.get("text", "")
                lo, hi = 0, len(text)
                while lo < hi:
                    mid = (lo + hi + 1) // 2
                    output["text"] = text[:mid]
                    if self.size + sep + cell_json_size(output) <= budget:
                        lo = mid
                    else:
                        hi = mid - 1
                output["text"] = text[:lo]
                if lo:
                    self.outputs.append(output)
            return
        self.outputs.append(output)
        self.size += sep + size

    def result(self):
        return self.outputs + [CELL_CUT] if self.cut else self.outputs


def as_text(value):
    return "".join(value) if isinstance(value, list) else str(value)


def display_outputs(outputs, data):
    for mime in ("image/png", "image/svg+xml"):
        if mime in data:
            value = as_text(data[mime])
            if mime == "image/svg+xml":
                value = base64.b64encode(value.encode("utf-8")).decode("ascii")
            else:
                # Jupyter allows line breaks inside base64 images.
                value = "".join(value.split())
            outputs.add({"type": "image", "mime": mime, "data": value})
            return
    text = as_text(data.get("text/plain", ""))
    other = [mime for mime in data if mime != "text/plain"]
    if other:
        for mime in other:
            outputs.add({"type": "text", "text": text + " (" + mime + " shown as text)"})
    elif "text/plain" in data:
        outputs.add({"type": "text", "text": text})


def retire_named():
    global named_session
    previous, named_session = named_session, None
    if previous is not None:
        _, km, kc = previous
        stop(km, kc)


def named_kernel(name):
    global named_session
    if not _dirs or name not in named_specs.find_kernel_specs():
        raise RuntimeError('no Jupyter kernel named "%s" is installed' % name)
    if named_session is not None and named_session[0] == name:
        return named_session[1:]
    retire_named()
    km = KernelManager(kernel_name=name, kernel_spec_manager=named_specs)
    # A spec may contribute environment variables, but it cannot restore the
    # daemon's private LYK_* values removed by the Coresearch host.
    km.kernel_spec.env = {k: v for k, v in km.kernel_spec.env.items() if not k.startswith("LYK_")}
    if name == "python3":
        km.kernel_spec.argv[0] = sys.executable
    kc = None
    named_session = (name, km, kc)
    try:
        env = {k: v for k, v in os.environ.items() if not k.startswith("LYK_")}
        km.start_kernel(cwd=cwd, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        kc = km.client()
        named_session = (name, km, kc)
        kc.start_channels()
        kc.wait_for_ready(timeout=START_TIMEOUT)
    except Exception:
        retire_named()
        raise
    return km, kc


def interrupt_cell(*_):
    global cell_interrupted, pending_interrupt
    if active_kernel is None:
        pending_interrupt = True
    else:
        cell_interrupted = True
        try:
            active_kernel.interrupt_kernel()
        except Exception:
            pass


def run_cell(name, code, timeout):
    global active_kernel, cell_interrupted, pending_interrupt
    outputs = CellOutputs()
    count, failed = 0, False
    cell_interrupted = False
    if pending_interrupt:
        pending_interrupt = False
        return [], 0, False, True
    km, kc = named_kernel(name)
    active_kernel = km
    try:
        if pending_interrupt or cell_interrupted:
            pending_interrupt = False
            return [], 0, False, True
        msg_id = kc.execute(code, allow_stdin=False)
        deadline = time.monotonic() + timeout
        while True:
            if time.monotonic() > deadline:
                interrupt_cell()
                retire_named()
                outputs.add({"type": "error", "ename": "TimeoutError", "evalue": TIME_LIMIT})
                return outputs.result(), count, False, cell_interrupted
            try:
                msg = kc.get_iopub_msg(timeout=0.2)
            except queue.Empty:
                if not km.is_alive():
                    retire_named()
                    outputs.add({"type": "error", "ename": "RuntimeError", "evalue": STOPPED})
                    return outputs.result(), count, False, cell_interrupted
                continue
            if msg["parent_header"].get("msg_id") != msg_id:
                continue
            kind, content = msg["msg_type"], msg["content"]
            if "execution_count" in content:
                count = content["execution_count"] or 0
            if kind == "stream":
                outputs.add({"type": "stream", "name": content.get("name", "stdout"), "text": as_text(content.get("text", ""))})
            elif kind in ("execute_result", "display_data"):
                display_outputs(outputs, content.get("data", {}))
            elif kind == "error":
                failed = True
                if content.get("ename") == "KeyboardInterrupt":
                    cell_interrupted = True
                outputs.add({"type": "error", "ename": content.get("ename", ""), "evalue": content.get("evalue", ""), "traceback": [ANSI.sub("", line) for line in content.get("traceback", [])]})
            elif kind == "status" and content.get("execution_state") == "idle":
                break
        ok = reply_status(kc, msg_id) == "ok" and not failed and not cell_interrupted
        return outputs.result(), count, ok, cell_interrupted
    finally:
        active_kernel = None



def shutdown(*_):
    retire_named()
    for km, kc in list(sessions.values()):
        stop(km, kc)
    sessions.clear()
    sys.exit(0)


def main():
    global pending_interrupt
    signal.signal(signal.SIGTERM, shutdown)
    signal.signal(signal.SIGHUP, shutdown)
    signal.signal(signal.SIGUSR1, interrupt_cell)
    # Cells wait for this frame before submission. Queued signals are now safe
    # and remain pending until a cell can consume them.
    replies.write(json.dumps({"ready": True}) + "\n")
    replies.flush()
    try:
        for line in sys.stdin:
            req = json.loads(line)
            if req.get("op") == "shutdown":
                break
            reply = {"id": req.get("id"), "stdout": "", "error": "", "ok": False, "restarted": False}
            try:
                if req.get("op") == "cell":
                    reply.update(cell_id=req.get("cell_id", ""), outputs=[], execution_count=0, interrupted=False)
                    out, count, ok, interrupted = run_cell(req.get("kernel"), req.get("code", ""), float(req.get("timeout", 600)))
                    reply.update(outputs=out, execution_count=count, ok=ok, interrupted=interrupted)
                elif req.get("op") == "restart_kernel":
                    name = req.get("kernel")
                    if not _dirs or name not in named_specs.find_kernel_specs():
                        raise RuntimeError('no Jupyter kernel named "%s" is installed' % name)
                    retire_named()
                    named_kernel(name)
                    pending_interrupt = False
                    reply.update(ok=True)
                elif req.get("op") == "restart":
                    # A language with no session yet has nothing to restart.
                    kernel_name(req.get("language"))
                    if req.get("language") in sessions:
                        restart(req.get("language"))
                    reply.update(ok=True, restarted=True)
                else:
                    out, err, ok, restarted = run(req.get("language"), req.get("code", ""), float(req.get("timeout", 600)))
                    reply.update(stdout=out, error=err, ok=ok, restarted=restarted)
            except Exception as e:
                if req.get("op") == "cell":
                    outputs = CellOutputs()
                    outputs.add({"type": "error", "ename": type(e).__name__, "evalue": str(e)})
                    reply.update(outputs=outputs.result(), ok=False)
                else:
                    reply["error"] = "%s: %s" % (type(e).__name__, e)
            replies.write(json.dumps(reply) + "\n")
            replies.flush()
    finally:
        shutdown()


if __name__ == "__main__":
    main()
