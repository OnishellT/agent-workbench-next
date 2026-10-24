"""Command-line entry point: service lifecycle, configuration, backups, sync, and candidate export/import."""
from __future__ import annotations
import argparse, contextlib, json, os, subprocess, sys, time, urllib.error, urllib.request, webbrowser
from pathlib import Path
from .common import Invalid, js
from .store import Store

def data_home():
    if os.environ.get('FOUNDRY_DATA'):return Path(os.environ['FOUNDRY_DATA']).expanduser()
    if os.name=='nt':return Path(os.environ.get('LOCALAPPDATA',Path.home()/'AppData'/'Local'))/'ConversationFoundry'
    return Path(os.environ.get('XDG_STATE_HOME',Path.home()/'.local'/'state'))/'conversation-foundry'

def metadata(root):
    try:return json.loads((root/'server.json').read_text())
    except(OSError,ValueError):return None

def request(root,path='/health',body=None):
    meta=metadata(root)
    if not meta:raise Invalid('El servicio no está iniciado. Ejecuta conversation-foundry start --background.')
    headers={}
    if path.startswith('/api/'):
        s=Store(root);headers['X-Foundry-Token']=s.get('token');s.db.close()
    if body is not None:headers['Content-Type']='application/json'
    req=urllib.request.Request('http://127.0.0.1:'+str(meta['port'])+path,data=js(body).encode()if body is not None else None,headers=headers)
    try:
        with urllib.request.urlopen(req,timeout=10)as r:return json.load(r)
    except urllib.error.HTTPError as e:
        try:message=json.load(e)['error']
        except Exception:message=str(e)
        raise Invalid(message)from None
    except OSError:raise Invalid('El servicio no responde. Ejecuta start --background; el estado permanece guardado.')from None

def running(root):
    try:
        health=request(root);meta=metadata(root)
        return health if isinstance(health,dict) and health.get('application')=='conversation-foundry' and meta and health.get('pid')==meta.get('pid') else None
    except Invalid:return None

def url(root):
    m=metadata(root);s=Store(root);token=s.get('token');s.db.close()
    return 'http://127.0.0.1:'+str(m['port'])+'/#token='+token

@contextlib.contextmanager
def singleton(root):
    root.mkdir(parents=True,exist_ok=True,mode=0o700)
    f=(root/'service.lock').open('a+')
    try:
        if os.name=='nt':
            import msvcrt
            f.write('0');f.flush();f.seek(0);msvcrt.locking(f.fileno(),msvcrt.LK_NBLCK,1)
        else:
            import fcntl
            fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB)
    except OSError:f.close();raise Invalid('Ya hay un worker usando este directorio de datos.')from None
    try:yield
    finally:f.close()

def main():
    parser=argparse.ArgumentParser(description='Conversation Foundry · ingesta y aprendizaje verificable')
    parser.add_argument('--data',type=Path,default=data_home(),help='Directorio de datos; también FOUNDRY_DATA.')
    sub=parser.add_subparsers(dest='command',required=True)
    start=sub.add_parser('start',help='Arrancar panel y worker local');start.add_argument('--background',action='store_true');start.add_argument('--port',type=int,default=1739);start.add_argument('--open',action='store_true')
    sub.add_parser('stop',help='Detener el servicio sin borrar datos');sub.add_parser('status');sub.add_parser('open',help='Abrir el panel con sesión local');sub.add_parser('doctor')
    call=sub.add_parser('call',help='Ejecutar una acción del API local');call.add_argument('payload',help='Objeto JSON con action; usa - para stdin')
    setup=sub.add_parser('configure',help='Configurar las fuentes y el proveedor');setup.add_argument('--discover',action='store_true',help='Registrar los directorios estándar locales de Codex/Claude/Pi/OMP, sin inferencia.');setup.add_argument('--provider',choices=['pi','codex','command']);setup.add_argument('--agent-command',help='Array JSON de argv; Pi requiere un único ejecutable');setup.add_argument('--model')
    setup.add_argument('--provider-id',help='Proveedor del modelo en Pi, por ejemplo openai-codex')
    setup.add_argument('--reasoning',choices=['off','minimal','low','medium','high','xhigh','max'],help='Nivel de razonamiento de Pi')
    setup.add_argument('--target',choices=['analysis','replay','both'],default='analysis',help='Configuración que cambia: analysis (predeterminado), replay o both; analysis conserva el agente de evaluación')
    sub.add_parser('backup');sub.add_parser('versions')
    catalog=sub.add_parser('catalog',help='Descubrir dominios y capacidades aceptadas sin cargar instrucciones');catalog.add_argument('--project')
    context=sub.add_parser('context',help='Leer sólo capacidades aceptadas aplicables');context.add_argument('--domain',required=True);context.add_argument('--project')
    sync=sub.add_parser('sync',help='Instalar capacidades aceptadas en un proyecto explícito');sync.add_argument('--domain',required=True);sync.add_argument('--project',required=True)
    run=sub.add_parser('run',help='Ejecutar tu agente con el arnés activo e ingerir la tarea');run.add_argument('--domain',required=True);run.add_argument('--project',required=True);run.add_argument('--prompt',required=True);run.add_argument('--case',action='append',default=[])
    sub.add_parser('repl',help='Abrir Python con el cliente Foundry para un loop RLM')
    export=sub.add_parser('export',help='Exportar un candidato como paquete Agent Skills generalizado e inspeccionable; exportar no aprueba');export.add_argument('--id',required=True)
    inspect=sub.add_parser('inspect',help='Mostrar manifiesto, revisión y estado de gate de un paquete exportado sin instalarlo');inspect.add_argument('package')
    imp=sub.add_parser('import',help='Instalar un paquete exportado como skill no administrada en .agents/skills/<name> de un proyecto, personalizada a este equipo');imp.add_argument('package');imp.add_argument('--project',required=True);imp.add_argument('--acknowledge-risk',action='store_true',help='Aceptar expresamente los hallazgos de riesgo del paquete tras revisarlos')
    args=parser.parse_args();root=args.data.expanduser().resolve()
    try:
        if args.command=='start':
            if running(root):
                print('Ya está iniciado: '+url(root))
                if args.open: webbrowser.open(url(root))
                return
            if args.background:
                root.mkdir(parents=True,exist_ok=True,mode=0o700)
                with (root/'service.log').open('ab')as log:
                    kwargs={'start_new_session':True}if os.name!='nt'else{'creationflags':subprocess.CREATE_NEW_PROCESS_GROUP|subprocess.DETACHED_PROCESS}
                    subprocess.Popen([sys.executable,'-m','foundry_loop','--data',str(root),'start','--port',str(args.port)],stdin=subprocess.DEVNULL,stdout=log,stderr=log,env=os.environ.copy(),**kwargs)
                for _ in range(60):
                    if running(root):break
                    time.sleep(.1)
                else:raise Invalid('No se pudo iniciar. Revisa '+str(root/'service.log'))
                print(url(root))
                if args.open:webbrowser.open(url(root))
            else:
                from .server import serve
                with singleton(root):serve(root,args.port,lambda meta:print('Foundry listo en puerto '+str(meta['port']),flush=True))
        elif args.command=='status':print(js({'running':bool(running(root)),'data':str(root),'server':metadata(root)}))
        elif args.command=='open':
            if not running(root):raise Invalid('Primero ejecuta start --background.')
            webbrowser.open(url(root));print(url(root))
        elif args.command=='stop':print(js(request(root,'/api/action',{'action':'shutdown'})))
        elif args.command=='call':
            body=json.loads(sys.stdin.read()if args.payload=='-'else args.payload)
            print(js(request(root,'/api/action',body)))
        elif args.command=='doctor':
            from .providers import diagnostics
            s=Store(root);print(js({'data':str(root),'running':bool(running(root)),**diagnostics(s.get('config'))}));s.db.close()
        elif args.command=='configure':
            s=Store(root);result={'sources':[]}
            if args.discover:
                from .ingest import discover_sources
                result.update(discover_sources(s))
            if any(value is not None for value in (args.provider,args.agent_command,args.model,args.provider_id,args.reasoning)):
                from .app import configure
                cfg=s.get('config');patch={}
                targets={'analysis':('provider',),'replay':('replay',),'both':('provider','replay')}[args.target]
                for target in targets:
                    p={**cfg[target]}
                    if args.provider:
                        if args.provider!=p['kind'] and args.agent_command is None:
                            if args.provider=='command':raise Invalid('Al seleccionar command, indica --agent-command con un array JSON de argumentos.')
                            p['command']=[args.provider]
                        p['kind']=args.provider
                    if args.agent_command is not None:p['command']=json.loads(args.agent_command)
                    if args.model is not None:p['model']=args.model
                    if args.provider_id is not None:p['provider']=args.provider_id
                    if args.reasoning is not None:p['reasoning']=args.reasoning
                    patch[target]=p
                s.set('config',configure(cfg,patch));result['configured_target']=args.target
            print(js(result));s.db.close()
        elif args.command=='backup':s=Store(root);print(s.backup());s.db.close()
        elif args.command=='catalog':
            from .integrations import active_catalog
            s=Store(root)
            try:print(js(active_catalog(s,args.project)))
            finally:s.db.close()
        elif args.command in ('context','sync','run'):
            from .integrations import active_context,sync_project
            s=Store(root)
            if args.command=='sync': result=sync_project(s,args.project,args.domain)
            elif args.command=='context': result=active_context(s,args.domain,args.project)
            else:
                from .client import run_task
                result=run_task(s,args.project,args.domain,args.prompt,args.case)
            print(js(result));s.db.close()
        elif args.command=='repl':
            import code
            from .client import Foundry
            code.interact(banner='Foundry RLM client listo: foundry.state(), foundry.call(action, **kwargs), foundry.wait(job_id).',local={'foundry':Foundry(root)})
        elif args.command=='export':
            from .publish import export_candidate
            s=Store(root)
            try:print(js(export_candidate(s,args.id)))
            finally:s.db.close()
        elif args.command=='inspect':
            from .publish import inspect_package
            manifest,contents=inspect_package(args.package)
            print(js({'manifest':{k:v for k,v in manifest.items() if k!='spec'},'name':manifest['spec']['name'],'description':manifest['spec'].get('description'),'files':sorted(contents),'skill':contents.get('skill/'+manifest['spec']['name']+'/SKILL.md')}))
        elif args.command=='import':
            from .publish import import_package
            s=Store(root)
            try:print(js(import_package(s,args.package,args.project,args.acknowledge_risk)))
            finally:s.db.close()
        elif args.command=='versions':s=Store(root);print(js({'active':s.get('active_version'),'versions':s.rows('SELECT * FROM versions ORDER BY created DESC')}));s.db.close()
    except(Invalid,ValueError,OSError,KeyError)as e:print('Error: '+str(e),file=sys.stderr);sys.exit(1)
    except KeyboardInterrupt:pass

if __name__=='__main__':main()
