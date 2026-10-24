"""The offline web build is served by the shipped Python runtime."""
import json
from html.parser import HTMLParser
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from urllib.error import HTTPError
from urllib.parse import urljoin
from urllib.request import urlopen
from unittest.mock import patch

from foundry_loop.server import web_asset

class Resources(HTMLParser):
    def __init__(self):super().__init__();self.urls=[]
    def handle_starttag(self,tag,attrs):
        a=dict(attrs)
        if tag=='script' and a.get('src'):self.urls.append(a['src'])
        if tag=='link' and a.get('rel') in ('stylesheet','modulepreload'):self.urls.append(a['href'])

class WebDeliveryTests(unittest.TestCase):
    def test_asset_boundary_rejects_traversal_and_external_symlinks(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);web=root/'web';(web/'assets').mkdir(parents=True)
            (root/'private.txt').write_text('private')
            (web/'assets'/'outside.js').symlink_to(root/'private.txt')
            (web/'assets'/'main.js').write_text('public')
            with patch('foundry_loop.server.WEB',web):
                self.assertEqual(web_asset('/assets/main.js').read_text(),'public')
                for path in ('/assets/../private.txt','/assets/%2e%2e/private.txt','/assets/outside.js','/assets/%00.js','/server.py'):
                    self.assertIsNone(web_asset(path),path)

    def test_built_entrypoint_and_referenced_assets_over_real_http(self):
        with tempfile.TemporaryDirectory() as root:
            code='import json,sys;from foundry_loop.server import serve;serve(sys.argv[1],0,lambda m:print(json.dumps(m),flush=True))'
            child=subprocess.Popen([sys.executable,'-u','-c',code,root],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
            try:
                base='http://127.0.0.1:'+str(json.loads(child.stdout.readline())['port'])
                with urlopen(base,timeout=5) as response:
                    self.assertIn("script-src 'self'",response.headers['Content-Security-Policy'])
                    parser=Resources();parser.feed(response.read().decode())
                self.assertTrue(parser.urls)
                for url in parser.urls:
                    self.assertFalse(url.startswith(('https:','http:','//')),url)
                    with urlopen(urljoin(base+'/',url),timeout=5) as response:
                        self.assertEqual(response.status,200)
                        self.assertGreater(len(response.read()),0)
                        if '.css' in url:self.assertIn('text/css',response.headers['Content-Type'])
                        if '.js' in url:self.assertIn('javascript',response.headers['Content-Type'])
                for url in ('/assets/%2e%2e/server.py','/server.py'):
                    with self.assertRaises(HTTPError) as error:urlopen(base+url,timeout=5)
                    self.assertEqual(error.exception.code,404);error.exception.close()
            finally:
                child.terminate();child.wait(timeout=7);child.stdout.close();child.stderr.close()
