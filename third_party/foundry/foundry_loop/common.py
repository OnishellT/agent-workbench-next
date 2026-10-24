"""Shared primitives: canonical JSON, digests, redaction-aware text cleaning, atomic writes and bounded subprocesses."""
from __future__ import annotations
import hashlib, json, os, signal, subprocess, tempfile, time
from pathlib import Path
from .vendor.foundry.redact import sanitize

class Invalid(ValueError): pass

def js(value): return json.dumps(value, ensure_ascii=False, sort_keys=True, allow_nan=False)
def digest(value): return hashlib.sha256(js(value).encode()).hexdigest()
def uid(prefix): return prefix + '_' + os.urandom(8).hex()
def now(): return time.time()
def clean(text):
    result = sanitize({'text': str(text)})
    if not result['safe']: raise Invalid('El contenido supera los límites de redacción.')
    return result['text']
def write_json(path, value):
    path = Path(path); path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile('w', dir=path.parent, delete=False, encoding='utf8') as f:
        os.chmod(f.name, 0o600); f.write(js(value)); f.flush(); os.fsync(f.fileno()); name=f.name
    os.replace(name, path)
def safe_relative(value):
    p=Path(value)
    if not value or p.is_absolute() or '..' in p.parts or '\\' in value or any(part.startswith('.') for part in p.parts):
        raise Invalid('La ruta del paquete debe ser relativa, sin directorios ocultos ni ..')
    return p

def process(argv, cwd, *, stdin='', timeout=180, env=None, cancel=None, max_output=4_000_000):
    """Bounded child process; kill its process group on timeout/cancellation."""
    if not isinstance(argv,list) or not argv or not all(isinstance(x,str) for x in argv):
        raise Invalid('El comando debe ser una lista de argumentos, sin shell.')
    start=now()
    with tempfile.TemporaryFile() as out, tempfile.TemporaryFile() as err, tempfile.TemporaryFile() as inp:
        inp.write(stdin.encode()); inp.seek(0)
        p=subprocess.Popen(argv,cwd=cwd,stdin=inp,stdout=out,stderr=err,env=env,
                           start_new_session=os.name!='nt')
        reason=None
        try:
            while p.poll() is None:
                if cancel and cancel(): reason='cancelled'; break
                if now()-start>timeout: reason='timeout'; break
                if os.fstat(out.fileno()).st_size+os.fstat(err.fileno()).st_size>max_output: reason='output_limit'; break
                time.sleep(.1)
        finally:
            if p.poll() is None:
                if os.name!='nt':
                    try: os.killpg(p.pid,signal.SIGKILL)
                    except ProcessLookupError: pass
                else: p.kill()
            p.wait()
            if os.name!='nt':
                try: os.killpg(p.pid,signal.SIGKILL)
                except ProcessLookupError: pass
        if os.fstat(out.fileno()).st_size+os.fstat(err.fileno()).st_size>max_output: reason='output_limit'
        out.seek(0); err.seek(0)
        return {'returncode':p.returncode,'stdout':out.read(max_output).decode('utf8','replace'),
                'stderr':err.read(max_output).decode('utf8','replace'),'seconds':now()-start,'error':reason}
