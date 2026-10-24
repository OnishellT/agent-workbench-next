"""Loopback HTTP server: token-authenticated JSON API plus the prebuilt offline UI under foundry_loop/web."""
from __future__ import annotations
import hmac, json, mimetypes, os, threading
from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs,urlsplit,unquote
from .app import App, decoded
from .common import Invalid, clean, js, now, write_json

WEB=Path(__file__).with_name('web')

def web_asset(path):
    """Serve only public entrypoints and build assets, never arbitrary package files."""
    asset=unquote(path).lstrip('/') if path!='/' else 'index.html'
    if '\\' in asset or '\x00' in asset or any(part in ('.','..') for part in asset.split('/')):
        return None
    if asset not in ('index.html','app.js','style.css','styles.css','favicon.svg') and not asset.startswith('assets/'):
        return None
    file=(WEB/asset).resolve()
    if not file.is_relative_to(WEB.resolve()) or not file.is_file():return None
    return file

class Server(ThreadingHTTPServer):
    daemon_threads=True
    allow_reuse_address=True

def serve(root,port=1739,ready=None):
    app=App(root);token=app.store.get('token')
    class Handler(BaseHTTPRequestHandler):
        def log_message(self,*args):pass
        def reply(self,status,body,ctype='application/json'):
            if isinstance(body,(dict,list)):body=js(body).encode()
            elif isinstance(body,str):body=body.encode()
            self.send_response(status);self.send_header('Content-Type',ctype);self.send_header('Content-Length',str(len(body)))
            self.send_header('Cache-Control','no-store');self.send_header('X-Content-Type-Options','nosniff')
            self.send_header('Content-Security-Policy',"default-src 'self'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'")
            self.end_headers()
            try:self.wfile.write(body)
            except(BrokenPipeError,ConnectionResetError):pass
        def host_ok(self):return self.headers.get('Host')in('127.0.0.1:'+str(server.server_port),'localhost:'+str(server.server_port))
        def auth(self):return self.host_ok()and hmac.compare_digest(self.headers.get('X-Foundry-Token',''),token)
        def do_GET(self):
            parts=urlsplit(self.path);path=parts.path;params=parse_qs(parts.query)
            if not self.host_ok():return self.reply(403,{'error':'Host no autorizado'})
            if path=='/health':return self.reply(200,{'application':'conversation-foundry','version':'1.0.0','pid':os.getpid()})
            if path.startswith('/api/'):
                if not self.auth():return self.reply(401,{'error':'Abre la aplicación desde conversation-foundry open para iniciar sesión local.'})
                try:
                    if path=='/api/state':return self.reply(200,app.state(compact=params.get('compact',['0'])[0]=='1'))
                    if path=='/api/candidate/readiness':return self.reply(200,app.readiness(params['id'][0]))
                    if path=='/api/episodes':
                        session_id=params['session_id'][0]
                        if not app.store.one('SELECT id FROM sessions WHERE id=?',(session_id,)):
                            raise Invalid('Conversación no encontrada.')
                        rows=app.store.rows('SELECT * FROM episodes WHERE session_id=? ORDER BY created DESC,id',(session_id,))
                        episodes=[decoded(app.store.enrich_episode(row)) for row in rows]
                        return self.reply(200,{'episodes':[row for row in episodes if not row['superseded']]})
                    if path=='/api/session':
                        from .episodes import session_detail_revision
                        return self.reply(200,session_detail_revision(app.store,params['id'][0],params.get('revision',[None])[0],compact=params.get('compact',['1'])[0]!='0'))
                    if path=='/api/export':
                        id=params.get('id',[''])[0]
                        if not __import__('re').fullmatch('candidate_[a-f0-9]+',id):raise Invalid('Id inválido.')
                        file=app.store.root/'exports'/(id+'.zip')
                        return self.reply(200,file.read_bytes(),'application/zip')
                    return self.reply(404,{'error':'Ruta desconocida'})
                except(KeyError,ValueError,OSError)as e:return self.reply(400,{'error':clean(str(e))})
            file=web_asset(path)
            if file is None:return self.reply(404,{'error':'Recurso no encontrado'})
            return self.reply(200,file.read_bytes(),mimetypes.guess_type(file.name)[0]or'application/octet-stream')
        def do_POST(self):
            origin=self.headers.get('Origin')
            allowed=('http://127.0.0.1:'+str(server.server_port),'http://localhost:'+str(server.server_port))
            if not self.auth()or(origin and origin not in allowed):return self.reply(403,{'error':'Sesión local u origen inválido'})
            if self.path!='/api/action':return self.reply(404,{'error':'Ruta desconocida'})
            try:
                length=int(self.headers.get('Content-Length','0'))
                if not 0<length<=2_000_000:raise Invalid('Tamaño de solicitud inválido.')
                body=json.loads(self.rfile.read(length));result=app.action(body);self.reply(200,result)
                if body.get('action')=='shutdown':threading.Thread(target=server.shutdown,daemon=True).start()
            except(Exception)as e:self.reply(400,{'error':clean(str(e))[:2500]})
    server=None
    try:
        server=Server(('127.0.0.1',port),Handler);server.app=app
        metadata={'port':server.server_port,'pid':os.getpid(),'started':now()};write_json(app.store.root/'server.json',metadata)
        if ready:ready(metadata)
    except BaseException:
        # No workers or HTTP handlers have started yet; startup failures can
        # safely release both resources without racing database users.
        if server is not None:server.server_close()
        app.store.db.close()
        raise
    app.start_worker()
    try:server.serve_forever(poll_interval=.25)
    finally:
        app.stop.set()
        app.store.execute("UPDATE jobs SET status='cancelled',error='Servicio detenido',updated=? WHERE status='running'",(now(),))
        if app.thread:app.thread.join(5)
        if app.capture_thread:app.capture_thread.join(5)
        server.server_close()
