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

replies = sys.stdout
sys.stdout = sys.stderr
cwd = os.getcwd()

# Kernels come only from the bases' kernelspec folders on JUPYTER_PATH, never
# from the person's own Jupyter setup.
_dirs = [os.path.join(p, "kernels") for p in os.environ.get("JUPYTER_PATH", "").split(os.pathsep) if p]
specs = KernelSpecManager(kernel_dirs=_dirs) if _dirs else KernelSpecManager()

sessions = {}


def kernel_name(language):
    if language == "python":
        return "python3"
    if language == "r":
        return "ir"
    if language == "julia":
        # IJulia names its kernelspec after the Julia version, julia-<major.minor>.
        names = sorted(n for n in specs.find_kernel_specs() if n.startswith("julia-"))
        if names:
            return names[-1]
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
    kc.start_channels()
    try:
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
    km, kc = sessions.pop(language)
    try:
        km.restart_kernel(now=True)
        kc.wait_for_ready(timeout=START_TIMEOUT)
        sessions[language] = (km, kc)
    except Exception:
        stop(km, kc)


def run(language, code, timeout):
    if language not in sessions:
        sessions[language] = start(language)
    km, kc = sessions[language]
    msg_id = kc.execute(code, allow_stdin=False)
    out, err, failed = [], [], False
    deadline = time.monotonic() + timeout
    while True:
        if time.monotonic() > deadline:
            try:
                km.interrupt_kernel()
            except Exception:
                pass
            restart(language)
            return clean(out), TIME_LIMIT, False, True
        try:
            msg = kc.get_iopub_msg(timeout=0.2)
        except queue.Empty:
            if not km.is_alive():
                restart(language)
                return clean(out), STOPPED, False, True
            continue
        if msg["parent_header"].get("msg_id") != msg_id:
            continue
        kind, content = msg["msg_type"], msg["content"]
        if kind == "stream":
            (err if content.get("name") == "stderr" else out).append(content.get("text", ""))
        elif kind in ("execute_result", "display_data"):
            text = content.get("data", {}).get("text/plain")
            if text is not None:
                out.append(text if text.endswith("\n") else text + "\n")
        elif kind == "error":
            failed = True
            err.append("%s: %s\n" % (content.get("ename", ""), content.get("evalue", "")))
            err.extend(line + "\n" for line in content.get("traceback", []))
        elif kind == "status" and content.get("execution_state") == "idle":
            break
    ok = reply_status(kc, msg_id) == "ok" and not failed
    return clean(out), clean(err), ok, False


def clean(parts):
    return ANSI.sub("", "".join(parts))


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
