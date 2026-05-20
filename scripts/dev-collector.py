#!/usr/bin/env python3
"""
Dev-only HTTP collector for skills-oci telemetry.

Stands up a stdlib HTTP server that accepts POST /v1/events, pretty-prints
each request, and returns a configurable status. Intended for local
verification of the producer (pkg/telemetry) against the wire contract.

Flags:
  --port N                Listen port (default 8787).
  --status N              Default HTTP status to return on success path
                          (default 202).
  --fail-first N          Return 500 for the first N requests, then fall
                          back to --status. Useful for exercising the
                          transient -> buffer -> drain path: pull N+1
                          skills, watch the (N+1)th success drain the
                          previously buffered events in FIFO order.
  --require-bearer TOKEN  If set, reject requests whose Authorization
                          header isn't exactly "Bearer TOKEN" (401).

Usage:
  python3 scripts/dev-collector.py
  python3 scripts/dev-collector.py --fail-first 3
  python3 scripts/dev-collector.py --status 400          # last-error.log path
  python3 scripts/dev-collector.py --require-bearer hunter2
"""

import argparse
import json
import sys
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer


class Collector(BaseHTTPRequestHandler):
    # Set by main() before serve_forever; readable from any handler instance.
    default_status = 202
    fail_first = 0
    required_bearer = ""
    received = 0  # class-level counter so --fail-first works across requests

    def do_POST(self):
        cls = type(self)
        cls.received += 1
        n = cls.received

        if self.path != "/v1/events":
            self._respond(404, f"#{n} 404 unexpected path={self.path!r}")
            return

        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length) if length else b""
        ct = self.headers.get("Content-Type", "")
        auth = self.headers.get("Authorization", "")

        if cls.required_bearer and auth != f"Bearer {cls.required_bearer}":
            self._respond(401, f"#{n} 401 bad/missing bearer (got {auth!r})")
            return

        if ct != "application/json":
            # Not a failure — just surface it so the producer's header
            # contract is visible during dev testing.
            print(f"  warning: Content-Type={ct!r}, expected application/json",
                  file=sys.stderr)

        try:
            evt = json.loads(body)
            summary = (
                f"event_type={evt.get('event_type')} "
                f"event_id={evt.get('event_id')} "
                f"command={evt.get('source', {}).get('command')} "
                f"trigger={evt.get('source', {}).get('trigger')} "
                f"skill={evt.get('skill', {}).get('namespace')}/"
                f"{evt.get('skill', {}).get('name')}@"
                f"{evt.get('skill', {}).get('version')}"
            )
        except json.JSONDecodeError as e:
            evt = None
            summary = f"<unparseable: {e}>"

        status = cls.default_status
        if cls.fail_first and n <= cls.fail_first:
            status = 500

        ts = datetime.now(timezone.utc).isoformat(timespec="seconds")
        print(f"[{ts}] #{n} -> {status}  {summary}")
        if evt is not None:
            print(json.dumps(evt, indent=2, sort_keys=False))
        else:
            print(body.decode("utf-8", "replace"))
        print("-" * 72)
        sys.stdout.flush()

        self._respond(status, log=None)

    def _respond(self, status, log=None):
        if log is not None:
            ts = datetime.now(timezone.utc).isoformat(timespec="seconds")
            print(f"[{ts}] {log}", file=sys.stderr)
            sys.stderr.flush()
        self.send_response(status)
        self.send_header("Content-Length", "0")
        self.end_headers()

    # Silence the default per-request access log; we print our own.
    def log_message(self, *args, **kwargs):
        return


def main():
    ap = argparse.ArgumentParser(
        description="Dev-only HTTP collector for skills-oci telemetry.",
    )
    ap.add_argument("--port", type=int, default=8787)
    ap.add_argument("--status", type=int, default=202,
                    help="Status returned on the success path (default 202).")
    ap.add_argument("--fail-first", type=int, default=0,
                    help="Return 500 for the first N requests.")
    ap.add_argument("--require-bearer", default="",
                    help="If set, require Authorization: Bearer <TOKEN>.")
    args = ap.parse_args()

    Collector.default_status = args.status
    Collector.fail_first = args.fail_first
    Collector.required_bearer = args.require_bearer

    srv = HTTPServer(("127.0.0.1", args.port), Collector)
    print(
        f"telemetry collector listening on http://127.0.0.1:{args.port}/v1/events",
        file=sys.stderr,
    )
    print(f"  default status: {args.status}", file=sys.stderr)
    if args.fail_first:
        print(f"  --fail-first: first {args.fail_first} requests return 500",
              file=sys.stderr)
    if args.require_bearer:
        print(f"  --require-bearer: requires 'Bearer {args.require_bearer}'",
              file=sys.stderr)
    print("ctrl-c to stop", file=sys.stderr)
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        srv.shutdown()


if __name__ == "__main__":
    main()
