"""Exercise the public HTTP API and real process restart; owned processes always stop."""
import json, os, pathlib, socket, subprocess, sys, tempfile, time, urllib.request
workspace=pathlib.Path(sys.argv[1]).resolve()
procs=[]
def start(data):
    with socket.socket() as sock: sock.bind(('127.0.0.1',0)); port=sock.getsockname()[1]
    env={**os.environ,'PORT':str(port),'DATA_FILE':str(data)}
    p=subprocess.Popen([sys.executable,str(workspace/'service.py')],cwd=workspace,env=env,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL);procs.append(p)
    for _ in range(100):
        if p.poll() is not None: raise RuntimeError('service failed to start')
        try:
            with socket.create_connection(('127.0.0.1',port),timeout=.1): return p,'http://127.0.0.1:'+str(port)
        except OSError: time.sleep(.03)
    raise RuntimeError('service did not become ready')
def stop(p):
    p.terminate()
    try:p.wait(timeout=3)
    except subprocess.TimeoutExpired:p.kill();p.wait()
try:
    with tempfile.TemporaryDirectory() as d:
        p,url=start(pathlib.Path(d)/'items.sqlite')
        item={'id':'acceptance-record','value':'persistencia real ñ'}
        req=urllib.request.Request(url+'/items',data=json.dumps(item).encode(),headers={'Content-Type':'application/json'},method='POST')
        with urllib.request.urlopen(req,timeout=3) as r: assert r.status==201
        with urllib.request.urlopen(url+'/items/'+item['id'],timeout=3) as r: assert json.load(r)==item
        if '--holdout' in sys.argv[2:]:
            item['value']='updated after second write ñ'
            req=urllib.request.Request(url+'/items',data=json.dumps(item).encode(),headers={'Content-Type':'application/json'},method='POST')
            with urllib.request.urlopen(req,timeout=3) as r: assert r.status==201
            extra={'id':'another-record','value':42}
            req=urllib.request.Request(url+'/items',data=json.dumps(extra).encode(),headers={'Content-Type':'application/json'},method='POST')
            with urllib.request.urlopen(req,timeout=3) as r: assert r.status==201
        stop(p);p,url=start(pathlib.Path(d)/'items.sqlite')
        with urllib.request.urlopen(url+'/items/'+item['id'],timeout=3) as r: assert json.load(r)==item
        if '--holdout' in sys.argv[2:]:
            with urllib.request.urlopen(url+'/items/another-record',timeout=3) as r: assert json.load(r)==extra
    print(json.dumps({'status':'pass','checks':['http_post','http_get','process_restart','data_persistence']}))
except Exception as e:
    print(json.dumps({'status':'fail','reason':str(e)}));sys.exit(1)
finally:
    for p in procs:
        if p.poll() is None:stop(p)
