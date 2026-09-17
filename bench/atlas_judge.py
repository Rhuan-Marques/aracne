#!/usr/bin/env python3
"""An OpenAI-compatible endpoint backed by the Claude Code CLI, for SWE-Atlas rubric grading.

WHY THIS EXISTS. SWE-Atlas grades a refactor with an LLM judge: `evaluate_rubrics.py` builds an
`openai.OpenAI(api_key, base_url)` client and asks it one `chat.completions` call per rubric
item. That needs an API key and an OpenAI-compatible endpoint, and this project has neither --
it runs on a Claude Code subscription, which is also how `bench/gen_descriptions.py` and
aracne's own lazy-description filler reach a model. This bridges the two: the judge keeps
speaking OpenAI, and the tokens come off the subscription.

It is deliberately tiny and deliberately local. It speaks exactly the one route the judge
calls, binds to loopback plus the docker bridge, and holds no credentials of its own -- the
`claude` CLI already has the session.

RUNNING UNDER DOCKER. The verifier runs inside the task container, so the container reaches
this through `host.docker.internal`, which the grader wires up with
`--add-host=host.docker.internal:host-gateway`. Bind to 0.0.0.0 for that to resolve; the port
is ephemeral and the server exits with the grading run.
"""
from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

CLAUDE_BIN = "claude"
# The judge asks for opus by an OpenAI-style id ("anthropic/claude-opus-4-5-…"); the CLI takes
# its own short aliases. Anything unrecognised is passed through for the CLI to resolve or
# reject, rather than silently downgraded -- a judge quietly run on a weaker model would make
# every score in the run incomparable.
ALIASES = {"opus": "opus", "sonnet": "sonnet", "haiku": "haiku"}


def to_cli_model(model: str) -> str:
    m = (model or "").lower()
    for name in ("opus", "sonnet", "haiku"):
        if name in m:
            return ALIASES[name]
    return model or "opus"


def strip_fences(text: str) -> str:
    """The CLI answers in prose+markdown; the judge parses JSON."""
    t = text.strip()
    fence = re.match(r"^```(?:json)?\s*\n(.*?)\n```\s*$", t, re.S)
    if fence:
        return fence.group(1).strip()
    return t


class JudgeError(RuntimeError):
    def __init__(self, status: int, message: str):
        super().__init__(message)
        self.status = status


def complete(model: str, messages: list[dict], wants_json: bool) -> str:
    """One chat completion through the Claude CLI. Shared by the HTTP shim (for the in-container
    verifier) and by atlas_rubric (which calls the official rubric code on the host, no HTTP)."""
    system = "\n\n".join(m.get("content") or "" for m in messages if m.get("role") == "system")
    user = "\n\n".join(m.get("content") or "" for m in messages if m.get("role") != "system")
    if wants_json:
        # The OpenAI flag has no CLI equivalent, so the requirement is stated in the
        # prompt. Without it the reply arrives wrapped in prose and the judge's parser
        # scores the rubric zero for a reason that has nothing to do with the code.
        system = (system + "\n\nRespond with a single valid JSON object and nothing else. "
                           "No prose, no markdown fences.").strip()
    cmd = [CLAUDE_BIN, "--print", "--model", to_cli_model(model)]
    if system:
        cmd += ["--append-system-prompt", system]
    try:
        proc = subprocess.run(cmd, input=user, capture_output=True, text=True, timeout=900)
    except subprocess.TimeoutExpired:
        raise JudgeError(504, "claude cli timed out")
    if proc.returncode != 0:
        detail = (proc.stderr or proc.stdout or "").strip()[:300]
        raise JudgeError(502, f"claude cli failed: {detail}")
    return strip_fences(proc.stdout or "")


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    calls = 0
    lock = threading.Lock()

    def log_message(self, *a):  # quiet; the grader owns the console
        pass

    def _send(self, code: int, payload: dict):
        body = json.dumps(payload).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):  # a health probe, and `/v1/models` for clients that look
        self._send(200, {"object": "list", "data": [{"id": "claude-cli", "object": "model"}]})

    def do_POST(self):
        if not self.path.rstrip("/").endswith("/chat/completions"):
            self._send(404, {"error": {"message": f"unsupported route {self.path}"}})
            return
        try:
            n = int(self.headers.get("Content-Length") or 0)
            req = json.loads(self.rfile.read(n) or b"{}")
        except Exception as e:
            self._send(400, {"error": {"message": f"bad request: {e}"}})
            return

        try:
            content = complete(req.get("model", ""), req.get("messages", []),
                               (req.get("response_format") or {}).get("type") == "json_object")
        except JudgeError as e:
            self._send(e.status, {"error": {"message": str(e)}})
            return
        with Handler.lock:
            Handler.calls += 1
            n_calls = Handler.calls
        print(f"  [judge] call {n_calls}: {len(content)} chars", file=sys.stderr, flush=True)

        self._send(200, {
            "id": f"chatcmpl-{uuid.uuid4().hex[:24]}",
            "object": "chat.completion",
            "created": int(time.time()),
            "model": req.get("model", "claude-cli"),
            "choices": [{"index": 0, "finish_reason": "stop",
                         "message": {"role": "assistant", "content": content}}],
            # The judge does not read usage, but a client that does should not crash.
            "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
        })


def serve(port: int = 0, host: str = "0.0.0.0") -> ThreadingHTTPServer:
    httpd = ThreadingHTTPServer((host, port), Handler)
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    return httpd


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--port", type=int, default=8765)
    ap.add_argument("--host", default="0.0.0.0")
    args = ap.parse_args()
    httpd = serve(args.port, args.host)
    print(f"judge shim on http://{args.host}:{httpd.server_address[1]}/v1  (ctrl-c to stop)")
    try:
        while True:
            time.sleep(3600)
    except KeyboardInterrupt:
        httpd.shutdown()
    return 0


if __name__ == "__main__":
    sys.exit(main())
