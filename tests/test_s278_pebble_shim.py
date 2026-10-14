import importlib.util
import json
import pathlib
import threading
import unittest
import urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer

ROOT = pathlib.Path(__file__).resolve().parent.parent


def load_shim():
    spec = importlib.util.spec_from_file_location("pebble_dns_shim", ROOT / "scripts" / "pebble-dns-shim.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class FakeChalltestsrv(BaseHTTPRequestHandler):
    calls = []

    def log_message(self, *args):
        pass

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        FakeChalltestsrv.calls.append((self.path, body))
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"{}")


def serve(handler):
    server = HTTPServer(("127.0.0.1", 0), handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


class PebbleShimTest(unittest.TestCase):
    """The shim speaks the Cloudflare subset the daemon's provider uses and
    mirrors TXT records into challtestsrv (S278 KVM validation only)."""

    def test_zone_records_and_txt_mirroring(self):
        shim = load_shim()
        challtest = serve(FakeChalltestsrv)
        FakeChalltestsrv.calls.clear()
        state = shim.State("pebble-lab.net", f"http://127.0.0.1:{challtest.server_port}")
        api = serve(shim.make_handler(state))
        base = f"http://127.0.0.1:{api.server_port}/client/v4"
        self.addCleanup(challtest.shutdown)
        self.addCleanup(api.shutdown)
        self.addCleanup(api.server_close)
        self.addCleanup(challtest.server_close)

        def call(method, path, body=None, auth=True):
            data = json.dumps(body).encode() if body is not None else None
            request = urllib.request.Request(base + path, data=data, method=method)
            if auth:
                request.add_header("Authorization", "Bearer x")
            request.add_header("Content-Type", "application/json")
            try:
                with urllib.request.urlopen(request, timeout=5) as response:
                    return response.status, json.loads(response.read())
            except urllib.error.HTTPError as error:
                return error.code, json.loads(error.read())

        status, body = call("GET", "/zones?name=pebble-lab.net")
        self.assertEqual(body["result"][0]["id"], shim.ZONE_ID)
        self.assertEqual(call("GET", "/zones?name=other.net")[1]["result"], [])
        self.assertEqual(call("GET", "/zones?name=pebble-lab.net", auth=False)[0], 404)

        record = {"type": "TXT", "name": "_acme-challenge.app.pebble-lab.net", "content": "tok", "ttl": 60}
        status, body = call("POST", f"/zones/{shim.ZONE_ID}/dns_records", record)
        rid = body["result"]["id"]
        self.assertEqual(FakeChalltestsrv.calls[-1],
                         ("/set-txt", {"host": "_acme-challenge.app.pebble-lab.net.", "value": "tok"}))
        listed = call("GET", f"/zones/{shim.ZONE_ID}/dns_records?page=1")[1]["result"]
        self.assertEqual([r["content"] for r in listed], ["tok"])

        self.assertEqual(call("DELETE", f"/zones/{shim.ZONE_ID}/dns_records/{rid}")[0], 200)
        self.assertEqual(FakeChalltestsrv.calls[-1],
                         ("/clear-txt", {"host": "_acme-challenge.app.pebble-lab.net."}))
        self.assertEqual(call("GET", f"/zones/{shim.ZONE_ID}/dns_records")[1]["result"], [])
        self.assertEqual(call("DELETE", f"/zones/{shim.ZONE_ID}/dns_records/{rid}")[0], 404)


if __name__ == "__main__":
    unittest.main()
