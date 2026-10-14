#!/usr/bin/env python3
"""Cloudflare-API shim over pebble-challtestsrv, for KVM validation only.

Homestead's DNS-01 flow writes challenge TXT records through a DNS
provider API. To validate issuance end to end without public DNS, point
the daemon's Cloudflare provider at this shim (HOMESTEAD_ACME_CLOUDFLARE_API)
and run Pebble with pebble-challtestsrv as its resolver. The shim speaks
the subset of the Cloudflare v4 API the provider uses (zone lookup,
record list, create, delete) and mirrors TXT records into challtestsrv's
management API (/set-txt, /clear-txt). Nothing here talks to the internet.
See docs/DEVELOPMENT.md, "Certificate first issuance (S278)".

Limit: challtestsrv holds one TXT value per host, which is all a single
certificate issuance needs.
"""
import argparse
import itertools
import json
import threading
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

ZONE_ID = "zone-shim"


class State:
    def __init__(self, zone, challtestsrv):
        self.zone = zone
        self.challtestsrv = challtestsrv.rstrip("/")
        self.records = {}
        self.ids = itertools.count(1)
        self.lock = threading.Lock()

    def challtest(self, path, host, value=None):
        body = {"host": host.rstrip(".") + "."}
        if value is not None:
            body["value"] = value
        request = urllib.request.Request(
            self.challtestsrv + path,
            data=json.dumps(body).encode(),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        urllib.request.urlopen(request, timeout=5).read()


def make_handler(state):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, fmt, *args):  # never log request bodies
            print("shim:", self.command, self.path.split("?")[0], flush=True)

        def reply(self, result, status=200):
            payload = {
                "success": True,
                "errors": [],
                "result": result,
                "result_info": {"page": 1, "total_pages": 1},
            }
            raw = json.dumps(payload).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def deny(self, status=404):
            raw = json.dumps({"success": False, "errors": [{"code": status, "message": "shim"}]}).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def route(self):
            parsed = urlparse(self.path)
            path = parsed.path
            prefix = "/client/v4"
            if not path.startswith(prefix) or not self.headers.get("Authorization"):
                return None, None
            return path[len(prefix):], parse_qs(parsed.query)

        def do_GET(self):
            path, query = self.route()
            if path == "/zones":
                match = query.get("name", [""])[0] == state.zone
                self.reply([{"id": ZONE_ID, "name": state.zone}] if match else [])
            elif path == f"/zones/{ZONE_ID}/dns_records":
                with state.lock:
                    self.reply(list(state.records.values()))
            else:
                self.deny()

        def do_POST(self):
            path, _ = self.route()
            if path != f"/zones/{ZONE_ID}/dns_records":
                return self.deny()
            body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
            with state.lock:
                record = {
                    "id": f"rec{next(state.ids)}",
                    "type": body["type"],
                    "name": body["name"],
                    "content": body["content"],
                    "ttl": body.get("ttl", 1),
                }
                state.records[record["id"]] = record
            if record["type"] == "TXT":
                state.challtest("/set-txt", record["name"], record["content"])
            self.reply(record)

        def do_DELETE(self):
            path, _ = self.route()
            prefix = f"/zones/{ZONE_ID}/dns_records/"
            if path is None or not path.startswith(prefix):
                return self.deny()
            with state.lock:
                record = state.records.pop(path[len(prefix):], None)
            if record is None:
                return self.deny()
            if record["type"] == "TXT":
                state.challtest("/clear-txt", record["name"])
            self.reply({"id": record["id"]})

    return Handler


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--zone", required=True, help="zone the shim serves, e.g. pebble-lab.net")
    parser.add_argument("--listen", default="127.0.0.1:8081")
    parser.add_argument("--challtestsrv", default="http://127.0.0.1:8055",
                        help="pebble-challtestsrv management URL")
    args = parser.parse_args()
    host, _, port = args.listen.rpartition(":")
    server = ThreadingHTTPServer((host, int(port)), make_handler(State(args.zone, args.challtestsrv)))
    print(f"shim: serving zone {args.zone} on {args.listen}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
