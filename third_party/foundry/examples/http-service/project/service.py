import json, os
from http.server import BaseHTTPRequestHandler, HTTPServer

items = {}
class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args): pass
    def do_POST(self):
        value=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        items[value['id']]=value
        self.send_response(201); self.end_headers(); self.wfile.write(b'ok')
    def do_GET(self):
        item=items.get(self.path.removeprefix('/items/'))
        self.send_response(200 if item else 404); self.send_header('Content-Type','application/json'); self.end_headers()
        self.wfile.write(json.dumps(item).encode())
HTTPServer(('127.0.0.1',int(os.environ['PORT'])),Handler).serve_forever()
