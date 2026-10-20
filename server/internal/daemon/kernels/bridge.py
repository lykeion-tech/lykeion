"""Runs cells in Jupyter kernels for the daemon.

Reads one JSON request per line on stdin and writes one JSON reply per line
on stdout. Only replies go to stdout: the kernels' own output goes to
/dev/null and anything this process would print goes to stderr.
"""

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

sessions = {}


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


def shutdown(*_):
    for km, kc in list(sessions.values()):
        stop(km, kc)
    sessions.clear()
    sys.exit(0)


def main():
    signal.signal(signal.SIGTERM, shutdown)
    signal.signal(signal.SIGHUP, shutdown)
    try:
        for line in sys.stdin:
            req = json.loads(line)
            if req.get("op") == "shutdown":
                break
            reply = {"id": req.get("id"), "stdout": "", "error": "", "ok": False, "restarted": False}
            try:
                out, err, ok, restarted = run(req.get("language"), req.get("code", ""), float(req.get("timeout", 600)))
                reply.update(stdout=out, error=err, ok=ok, restarted=restarted)
            except Exception as e:
                reply["error"] = "%s: %s" % (type(e).__name__, e)
            replies.write(json.dumps(reply) + "\n")
            replies.flush()
    finally:
        shutdown()


if __name__ == "__main__":
    main()
