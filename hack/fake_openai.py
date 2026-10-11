"""Minimal OpenAI-compatible /v1/chat/completions stub for offline wiring tests.

It lets the real, unmodified OpenJev server run without network access: OpenJev
sends its "calibrated probability estimator" prompt, and this stub answers
{"p": ...} using the same fixed rules as `jev-gate fake-decider`. It proves the
plumbing (gate -> OpenJev -> OpenAI-compatible API -> back), not model quality.
"""
import json
import re
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer


def block_probability(state: dict) -> float:
    c, s = state.get("canary", {}), state.get("stable", {})
    slo = state.get("slo", {}).get("p95_latency_ms_max", 0)
    if c.get("success_rate", 1) < 0.90:
        return 0.95
    if slo and c.get("p95_ms", 0) > slo:
        return 0.90
    if c.get("p95_ms", 0) > 20 * max(s.get("p95_ms", 0), 1):
        return 0.50
    return 0.05


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        user = next(m["content"] for m in body["messages"] if m["role"] == "user")
        m = re.search(r"STATE:\n(.*)\n\nSTATEMENT:", user, re.S)
        try:
            p = block_probability(json.loads(m.group(1)))
        except Exception:
            p = 0.5
        resp = {
            "id": "stub", "object": "chat.completion", "model": body.get("model", "stub"),
            "choices": [{"index": 0, "finish_reason": "stop",
                         "message": {"role": "assistant", "content": json.dumps({"p": p})}}],
            "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
        }
        out = json.dumps(resp).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def log_message(self, *a):
        pass


if __name__ == "__main__":
    HTTPServer(("127.0.0.1", int(sys.argv[1]) if len(sys.argv) > 1 else 9400), Handler).serve_forever()
