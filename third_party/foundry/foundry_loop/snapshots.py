"""Portable, bounded snapshots and frozen verifier controls.

Replay subprocesses run as the local user, not in a security sandbox. Hash guards
protect evaluator integrity against accidental edits; hostile agents need an OS
sandbox with a separately mounted read-only verifier directory.
"""
from __future__ import annotations
import hashlib, json, os, shutil, sys, tarfile, tempfile
from pathlib import Path
from .common import Invalid, clean, digest, js, now, process, uid, write_json

EXCLUDED={'.git','.hg','.svn','.env','.venv','venv','node_modules','__pycache__','.pytest_cache','.mypy_cache','dist','build','target','.foundry-skills'}
SECRET_SUFFIXES={'.pem','.key','.p12','.pfx','.sqlite','.sqlite3'}

def tree_hash(root):
    if not Path(root).is_dir(): raise Invalid('Falta el directorio del snapshot o del verificador.')
    rows=[]
    for p in sorted(Path(root).rglob('*')):
        if p.is_symlink(): raise Invalid('Un snapshot no puede contener enlaces simbólicos.')
        if p.is_file(): rows.append([p.relative_to(root).as_posix(),hashlib.sha256(p.read_bytes()).hexdigest(),bool(p.stat().st_mode & 0o111)])
    return digest(rows)

def _excluded(name):
    return name in EXCLUDED or name.startswith('.env.') or name.lower() in {'credentials','credentials.json','secrets.json','id_rsa','id_ed25519'} or Path(name).suffix.lower() in SECRET_SUFFIXES

def copy_source(source,dest,max_bytes=100_000_000,max_files=10000,exclude=()):
    total=count=0; omitted=[]
    for current,dirs,files in os.walk(source,followlinks=False):
        current=Path(current); relative=current.relative_to(source)
        kept=[]
        for name in dirs:
            p=current/name
            if _excluded(name) or p.is_symlink() or (relative/name).as_posix() in exclude: omitted.append((relative/name).as_posix())
            else: kept.append(name)
        dirs[:]=kept
        (dest/relative).mkdir(parents=True,exist_ok=True)
        for name in files:
            p=current/name
            if _excluded(name) or p.is_symlink() or (relative/name).as_posix() in exclude: omitted.append((relative/name).as_posix()); continue
            if not p.is_file(): continue
            count+=1; total+=p.stat().st_size
            if total>max_bytes or count>max_files: raise Invalid('Snapshot excede el límite de 100 MB o 10000 archivos.')
            shutil.copy2(p,dest/relative/name)
    return {'files':count,'bytes':total,'omitted':omitted}

def expand(argv,workspace,verifier):
    return [x.replace('{workspace}',str(workspace)).replace('{verifier}',str(verifier)).replace('{python}',sys.executable) for x in argv]

def _argv(value,name,required=True):
    if value is None and not required: return []
    if not isinstance(value,list) or not value or not all(isinstance(x,str) and x for x in value): raise Invalid(name+' debe ser una lista JSON de argumentos sin shell.')
    return value

def verifier_run(argv,workspace,verifier,timeout=60,cancel=None):
    try:
        env=dict(os.environ); env['PYTHONDONTWRITEBYTECODE']='1'
        result=process(expand(argv,workspace,verifier),workspace,timeout=timeout,cancel=cancel,max_output=500_000,env=env)
        result['stdout']=clean(result['stdout'][:8000]); result['stderr']=clean(result['stderr'][:8000])
        result['status']='infrastructure_error' if result['error'] or result['returncode'] in (126,127) else ('pass' if result['returncode']==0 else 'fail')
        return result
    except (OSError,Invalid) as exc:
        return {'status':'infrastructure_error','error':str(exc),'returncode':None,'stdout':'','stderr':'','seconds':0}

def freeze_argv(argv,project,verifier):
    """Freeze file/directory arguments located in the project, including argv[0]."""
    result=[]
    for arg in argv:
        raw=arg.replace('{workspace}',str(project))
        candidate=Path(raw)
        if not candidate.is_absolute(): candidate=project/candidate
        try: rel=candidate.resolve().relative_to(project.resolve())
        except ValueError:
            if Path(raw).is_absolute() and Path(raw).is_file() and arg!=argv[0]:
                rel=Path('external')/hashlib.sha256(raw.encode()).hexdigest()[:12]/Path(raw).name
                target=verifier/rel; target.parent.mkdir(parents=True,exist_ok=True); shutil.copy2(raw,target)
                result.append('{verifier}/'+rel.as_posix()); continue
            result.append(arg); continue
        if candidate.exists() and str(rel)!='.':
            target=verifier/rel; target.parent.mkdir(parents=True,exist_ok=True)
            if candidate.is_dir(): shutil.copytree(candidate,target,dirs_exist_ok=True)
            else: shutil.copy2(candidate,target)
            result.append('{verifier}/'+rel.as_posix())
        else: result.append(arg)
    return result

def create_case(store,body,cancel=None):
    if cancel and cancel(): raise Invalid('Creación de caso cancelada.')
    project=Path(body.get('project_path',body.get('path',''))).expanduser().resolve()
    if not body.get('project_path',body.get('path')) or not project.is_dir(): raise Invalid('Indica un project_path existente.')
    if project==store.root or project in store.root.parents or store.root in project.parents: raise Invalid('El proyecto debe estar separado del almacén de Foundry.')
    title=clean(body.get('title','')).strip(); prompt=clean(body.get('prompt','')).strip()
    domain=clean(body.get('domain','')).strip(); split=body.get('split','origin')
    if not title or not prompt or not domain: raise Invalid('title, prompt y domain son obligatorios.')
    if split not in ('origin','holdout','regression'): raise Invalid('split debe ser origin, holdout o regression.')
    verify=_argv(body.get('verify'),'verify'); negative=_argv(body.get('negative_control',body.get('negative')),'negative_control')
    positive=_argv(body.get('positive_control'),'positive_control',False)
    id=uid('case'); target=store.root/'snapshots'/id; target.mkdir()
    try:
        source=target/'project'; source.mkdir(); verifier=target/'verifier'; verifier.mkdir()
        ref=body.get('git_ref')
        if ref:
            # git archive reads a requested committed state, never the final dirty tree.
            result=process(['git','rev-parse','--verify',str(ref)+'^{commit}'],project,cancel=cancel)
            if result['returncode'] or result['error']: raise Invalid('git_ref no es un commit válido.')
            commit=result['stdout'].strip()
            archive=target/'source.tar'
            result=process(['git','archive','--format=tar','--output='+str(archive),commit],project,cancel=cancel)
            if result['returncode'] or result['error']: raise Invalid('No se pudo capturar el commit.')
            with tempfile.TemporaryDirectory() as td:
                tmp=Path(td)
                with tarfile.open(archive) as tar:
                    members=tar.getmembers()
                    if len(members)>10000 or sum(m.size for m in members)>100_000_000: raise Invalid('Commit demasiado grande.')
                    for m in members:
                        p=Path(m.name)
                        if p.is_absolute() or '..' in p.parts or not (m.isfile() or m.isdir()): continue
                        destination=tmp/p
                        if m.isdir(): destination.mkdir(parents=True,exist_ok=True); continue
                        destination.parent.mkdir(parents=True,exist_ok=True)
                        with tar.extractfile(m) as inp, destination.open('wb') as out: shutil.copyfileobj(inp,out)
                        destination.chmod(0o755 if m.mode & 0o111 else 0o644)
                coverage=copy_source(tmp,source,exclude=body.get('exclude',[]))
            archive.unlink()
        else: commit=None; coverage=copy_source(project,source,exclude=body.get('exclude',[]))
        if body.get('verifier_path'):
            oracle=Path(body['verifier_path']).expanduser().resolve()
            if not oracle.is_dir() or oracle==project or oracle in project.parents or project in oracle.parents or oracle==store.root or oracle in store.root.parents or store.root in oracle.parents:
                raise Invalid('verifier_path debe ser un directorio separado del proyecto.')
            copy_source(oracle,verifier)
        frozen=freeze_argv([x.replace(str(project),str(source)) for x in verify],source,verifier)
        positive=[x.replace(str(project),'{workspace}') for x in positive]
        negative=[x.replace(str(project),'{workspace}') for x in negative]
        frozen_hash=tree_hash(verifier)
        controls=[]
        with tempfile.TemporaryDirectory(prefix='foundry-controls-') as td:
            workspace=Path(td)/'workspace'; shutil.copytree(source,workspace)
            if positive:
                prep=verifier_run(positive,workspace,verifier,cancel=cancel); controls.append({'phase':'positive_setup',**prep})
                if prep['status']!='pass': raise Invalid('El control positivo no se pudo preparar: '+js(prep))
            good=verifier_run(frozen,workspace,verifier,cancel=cancel); controls.append({'phase':'positive_verify',**good})
            if good['status']!='pass': raise Invalid('El verificador debe aprobar el control positivo. Configura positive_control: '+js(good))
            bad_setup=verifier_run(negative,workspace,verifier,cancel=cancel); controls.append({'phase':'negative_setup',**bad_setup})
            if bad_setup['status']!='pass': raise Invalid('El control negativo debe preparar el defecto y terminar con código 0: '+js(bad_setup))
            bad=verifier_run(frozen,workspace,verifier,cancel=cancel); controls.append({'phase':'negative_verify',**bad})
            if bad['status']!='fail': raise Invalid('El verificador no detectó el defecto del control negativo: '+js(bad))
        if tree_hash(verifier)!=frozen_hash: raise Invalid('Un control modificó el verificador congelado.')
        manifest={'project_key':digest(str(project)),'schema':1,'directory':'snapshots/'+id,'project_hash':tree_hash(source),'verifier_hash':tree_hash(verifier),'git_commit':commit,'coverage':coverage,'controls':controls,'positive_control':positive,'verify':frozen,'negative_control':negative,'created':now()}
        write_json(target/'manifest.json',manifest)
        row={'id':id,'title':title,'prompt':prompt,'snapshot':js(manifest),'verify_json':js(frozen),'negative_json':js(negative),'domain':domain,'split':split,'created':now()}
        if cancel and cancel(): raise Invalid('Creación de caso cancelada; no se guardaron resultados.')
        store.execute('INSERT INTO cases VALUES (?,?,?,?,?,?,?,?,?)',tuple(row.values())); store.audit('case.created',{'id':id,'manifest':digest(manifest)})
        return row
    except BaseException:
        shutil.rmtree(target,ignore_errors=True); raise

def case_paths(store,case):
    manifest=json.loads(case['snapshot']); root=store.root/manifest['directory']
    if not root.resolve().is_relative_to((store.root/'snapshots').resolve()): raise Invalid('Snapshot fuera del almacén.')
    if tree_hash(root/'project')!=manifest['project_hash'] or tree_hash(root/'verifier')!=manifest['verifier_hash']: raise Invalid('La integridad del snapshot o del verificador cambió.')
    return root/'project',root/'verifier'
