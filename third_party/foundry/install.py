#!/usr/bin/env python3
"""Install Conversation Foundry using only Python's standard library.

Runtime copies are immutable; the active pointer switches atomically. Portable
installs resolve every bundled path relative to the launcher's actual location.
"""
from __future__ import annotations
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time

APP = 'conversation-foundry'
MARKER = 'conversation-foundry-owned-v1'
ROOT = Path(__file__).resolve().parent
BUNDLES = ('foundry_loop', 'ui', 'skills', 'examples', 'docs', 'README.md', 'README.en.md', 'LICENSE', 'NOTICE', 'VENDOR_PROVENANCE.json', 'install.py', 'install.sh')
LAUNCHER = '''#!/usr/bin/env python3
# conversation-foundry-owned-v1
import json, os, pathlib, sys
prefix = pathlib.Path(__file__).resolve().parent.parent
home = prefix / "share" / "conversation-foundry"
config = json.loads((home / "active.json").read_text())
release = home / "releases" / config["release"]
args = sys.argv[1:]
if "--data" not in args and not any(a.startswith("--data=") for a in args):
    data = str(prefix / "var" / "conversation-foundry") if config.get("portable") else config["data"]
    args = ["--data", data, *args]
env = os.environ.copy()
env["PATH"] = str(prefix / "bin") + os.pathsep + env.get("PATH", os.defpath)
env["PYTHONSAFEPATH"] = "1"
env["PYTHONPATH"] = str(release) + (os.pathsep + env["PYTHONPATH"] if env.get("PYTHONPATH") else "")
os.execve(sys.executable, [sys.executable, "-m", "foundry_loop", *args], env)
'''

def atomic_json(path, value):
    fd, name = tempfile.mkstemp(dir=path.parent, prefix='.active-')
    try:
        with os.fdopen(fd, 'w', encoding='utf8') as out:
            json.dump(value, out, indent=2); out.write('\n'); out.flush(); os.fsync(out.fileno())
        os.replace(name, path)
    finally:
        if os.path.exists(name): os.unlink(name)

def source_files(root):
    excluded={'node_modules','__pycache__','.git','.vite','coverage','dist'}
    for name in BUNDLES:
        item = root / name
        if item.is_symlink(): raise ValueError('Bundle contains a symbolic link: ' + str(item))
        if item.is_file(): yield item
        elif item.is_dir():
            for directory,dirs,files in os.walk(item,followlinks=False):
                dirs[:]=sorted(d for d in dirs if d not in excluded)
                for entry in dirs:
                    if (Path(directory)/entry).is_symlink():raise ValueError('Bundle contains a symbolic link: '+str(Path(directory)/entry))
                for filename in sorted(files):
                    path=Path(directory)/filename
                    if path.suffix in ('.pyc','.pyo'):continue
                    if path.is_symlink():raise ValueError('Bundle contains a symbolic link: '+str(path))
                    yield path

def install(prefix, *, portable=False, data=None, source=ROOT):
    if not (source/'foundry_loop'/'__main__.py').exists():
        raise ValueError('Source package is incomplete: foundry_loop/__main__.py is missing.')
    if portable and data:
        raise ValueError('--portable manages data inside PREFIX/var; omit --data.')
    prefix=Path(prefix).expanduser().resolve(); home=prefix/'share'/APP
    launcher=prefix/'bin'/APP
    if launcher.exists() and MARKER not in launcher.read_text(encoding='utf8', errors='replace'):
        raise ValueError('Refusing to replace a launcher not owned by Conversation Foundry: '+str(launcher))
    if home.exists() and any(home.iterdir()) and (not (home/'.owned').is_file() or (home/'.owned').read_text().strip()!=MARKER):
        raise ValueError('Refusing to replace an unowned installation directory: '+str(home))
    files=list(source_files(source)); h=hashlib.sha256()
    for path in files:
        h.update(str(path.relative_to(source)).encode()); h.update(b'\0'); h.update(path.read_bytes()); h.update(b'\0')
    release='v1-'+h.hexdigest()[:16]
    home.mkdir(parents=True,exist_ok=True); (home/'.owned').write_text(MARKER+'\n')
    releases=home/'releases'; releases.mkdir(exist_ok=True); dest=releases/release
    if not dest.exists():
        staging=Path(tempfile.mkdtemp(prefix='.install-',dir=releases))
        try:
            for path in files:
                target=staging/path.relative_to(source); target.parent.mkdir(parents=True,exist_ok=True)
                shutil.copy2(path,target)
            os.replace(staging,dest)
        finally:
            if staging.exists(): shutil.rmtree(staging)
    old={}
    if (home/'active.json').exists(): old=json.loads((home/'active.json').read_text())
    # Updates retain the established data path unless the user explicitly changes it.
    data_path=Path(data).expanduser().resolve() if data else Path(old.get('data') or str(Path.home()/'.local'/'share'/APP/'data'))
    if portable: data_path=prefix/'var'/APP
    if data_path.resolve().is_relative_to(releases.resolve()) or data_path.resolve().is_relative_to(launcher.parent.resolve()):
        raise ValueError('Data must be separate from installed runtime releases and launchers.')
    data_path.mkdir(parents=True,exist_ok=True,mode=0o700)
    config={'release':release,'previous_release':old.get('release'),'portable':portable,'data':str(data_path) if not portable else None}
    launcher.parent.mkdir(parents=True,exist_ok=True)
    fd,tmp=tempfile.mkstemp(dir=launcher.parent,prefix='.foundry-launch-')
    with os.fdopen(fd,'w',encoding='utf8') as out: out.write(LAUNCHER)
    os.chmod(tmp,0o755); os.replace(tmp,launcher)
    atomic_json(home/'active.json',config)
    return launcher,config

SKILL_NAME='foundry-learning-loop'
SKILL_MARKER='.foundry-owned'

def agent_skill_targets():
    home=Path.home()
    for name,root in [('Codex',home/'.codex'),('Claude Code',home/'.claude'),('Pi',home/'.pi'/'agent'),('OMP',home/'.omp'/'agent')]:
        if root.is_dir(): yield name,root/'skills'/SKILL_NAME

def skill_digest(root):
    h=hashlib.sha256()
    for path in sorted(Path(root).rglob('*')):
        if path.name==SKILL_MARKER and path.parent==Path(root): continue
        if path.is_symlink(): return None
        if path.is_file():
            h.update(path.relative_to(root).as_posix().encode());h.update(b'\0');h.update(path.read_bytes());h.update(b'\0')
    return h.hexdigest()

def owned_skill(path,prefix):
    if path.is_symlink(): return False
    try: marker=json.loads((path/SKILL_MARKER).read_text())
    except (OSError,ValueError): return False
    return isinstance(marker,dict) and isinstance(marker.get('digest'),str) and marker.get('owner')==MARKER and marker.get('prefix')==str(Path(prefix).resolve()) and marker['digest']==skill_digest(path)

def install_skills(prefix,release):
    prefix=Path(prefix).resolve();source=Path(release)/'skills'/SKILL_NAME
    if not (source/'SKILL.md').is_file(): raise ValueError('The release has no bundled bootstrap skill.')
    results=[]
    for name,dest in agent_skill_targets():
        if dest.exists() and not owned_skill(dest,prefix):
            results.append(name+': preserved existing foreign or edited skill at '+str(dest));continue
        dest.parent.mkdir(parents=True,exist_ok=True)
        staging=Path(tempfile.mkdtemp(prefix='.foundry-skill-',dir=dest.parent));previous=None
        try:
            shutil.copytree(source,staging,dirs_exist_ok=True)
            value=skill_digest(staging)
            if value is None: raise ValueError('Bootstrap skill contains a symbolic link.')
            atomic_json(staging/SKILL_MARKER,{'owner':MARKER,'prefix':str(prefix),'digest':value})
            if dest.exists():
                previous=Path(tempfile.mkdtemp(prefix='.foundry-previous-',dir=dest.parent));previous.rmdir();os.replace(dest,previous)
            try: os.replace(staging,dest)
            except BaseException:
                if previous is not None: os.replace(previous,dest);previous=None
                raise
            results.append(name+': installed bootstrap skill at '+str(dest))
        finally:
            if staging.exists(): shutil.rmtree(staging)
            if previous is not None and previous.exists(): shutil.rmtree(previous)
    return results or ['No existing supported agent directories found; bootstrap remains available in the release.']

def uninstall_skills(prefix):
    for name,path in agent_skill_targets():
        if path.exists() and owned_skill(path,prefix): shutil.rmtree(path)

def uninstall(prefix):
    prefix=Path(prefix).expanduser().resolve(); home=prefix/'share'/APP; launcher=prefix/'bin'/APP
    if home.exists() and (not (home/'.owned').is_file() or (home/'.owned').read_text().strip()!=MARKER):
        raise ValueError('Installation marker is absent or invalid; refusing deletion.')
    if launcher.exists():
        if MARKER not in launcher.read_text(encoding='utf8',errors='replace'):
            raise ValueError('Launcher is not owned by Conversation Foundry; refusing deletion.')
        launcher.unlink()
    if home.exists():
        # The standard data directory is home/data: never remove the parent tree.
        releases=home/'releases'
        if releases.is_dir(): shutil.rmtree(releases)
        active=home/'active.json'
        if active.is_file(): active.unlink()
        # Keep the ownership marker alongside retained data so reinstall is safe.
        if set(p.name for p in home.iterdir())=={'.owned'}:
            (home/'.owned').unlink();home.rmdir()
    uninstall_skills(prefix)
    for desktop in desktop_paths():
        if desktop.exists() and owns_desktop(desktop, prefix): desktop.unlink()
    return prefix/'var'/APP

def desktop_paths():
    return [Path(os.environ.get('XDG_CONFIG_HOME',Path.home()/'.config'))/'autostart'/f'{APP}.desktop',
            Path(os.environ.get('XDG_DATA_HOME',Path.home()/'.local'/'share'))/'applications'/f'{APP}.desktop']

def owns_desktop(path,prefix):
    content=path.read_text(encoding='utf8')
    owner='X-Foundry-Owner='+json.dumps(str(Path(prefix).resolve()),ensure_ascii=True)
    if MARKER not in content: return False
    if owner in content.splitlines(): return True
    if 'X-Foundry-Owner=' in content: return False
    launcher=str(Path(prefix).resolve()/'bin'/APP)
    quoted='"'+launcher.replace('\\','\\\\').replace('"','\\"').replace('`','\\`').replace('$','\\$')+'"'
    return ('Exec='+quoted+' start --background') in content.splitlines()

def desktop_entry(launcher, *, login=False):
    if not sys.platform.startswith('linux'): raise ValueError('Desktop integration currently supports Linux desktop sessions only.')
    launcher=Path(launcher).resolve(); prefix=launcher.parent.parent
    path=desktop_paths()[0 if login else 1]
    if path.exists() and not owns_desktop(path,prefix):
        raise ValueError('Refusing to overwrite an unrelated desktop entry: '+str(path))
    # Quote a Desktop Entry argument, including percent field-code escaping.
    raw=str(launcher).replace('%','%%')
    quoted='"'+raw.replace('\\','\\\\').replace('"','\\"').replace('`','\\`').replace('$','\\$')+'"'
    command=quoted+' start --background'+('' if login else ' --open')
    content='\n'.join(['[Desktop Entry]','Type=Application','Name=Conversation Foundry',
        'Comment='+MARKER,'Exec='+command,'Terminal=false','Icon=applications-development',
        'Categories=Development;Utility;','X-Foundry-Owner='+json.dumps(str(prefix),ensure_ascii=True),
        'X-GNOME-Autostart-enabled=true' if login else 'StartupNotify=false',''])
    path.parent.mkdir(parents=True,exist_ok=True);path.write_text(content,encoding='utf8')
    return path

def autostart(launcher): return desktop_entry(launcher,login=True)

def restart_server(launcher):
    """Only ask the authenticated service for this launcher's data directory to stop.

    Never signal a PID or kill a port occupant. A failed status/stop aborts startup.
    """
    command=[sys.executable,str(launcher)]
    def status():
        result=subprocess.run(command+['status'],capture_output=True,text=True)
        if result.returncode: raise ValueError('Could not inspect the installed service: '+result.stderr.strip())
        try: value=json.loads(result.stdout)
        except ValueError: raise ValueError('Installed service returned an invalid status.') from None
        if not isinstance(value,dict) or type(value.get('running')) is not bool: raise ValueError('Installed service returned an invalid status.')
        return value
    current=status(); port=None
    if current['running']:
        server=current.get('server') or {};port=server.get('port')
        print('Restarting the existing Foundry service for this data directory…')
        stopped=subprocess.run(command+['stop'],capture_output=True,text=True)
        if stopped.returncode: raise ValueError('Could not stop the owned service: '+stopped.stderr.strip())
        for _ in range(100):
            if not status()['running']: break
            time.sleep(.1)
        else: raise ValueError('The service did not stop. Runtime is installed; check status before starting it again.')
    args=command+['start','--background']
    if type(port) is int and 1<=port<=65535: args+=['--port',str(port)]
    return subprocess.run(args).returncode

def main(argv=None):
    parser=argparse.ArgumentParser(description=__doc__,formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--prefix',default=str(Path.home()/'.local'),help='Installation prefix (default: ~/.local)')
    parser.add_argument('--portable',action='store_true',help='Keep data in PREFIX/var and resolve paths after relocation')
    parser.add_argument('--data',help='Explicit data directory for a nonportable install')
    parser.add_argument('--start',action='store_true',help='Start the installed server; gracefully restart an existing instance for the same data directory')
    parser.add_argument('--dry-run',action='store_true',help='Show the installation mode and planned paths without writing or starting anything')
    parser.add_argument('--skills',action='store_true',help='Install the bundled bootstrap skill into existing local agent directories; preserve foreign or edited skills')
    parser.add_argument('--autostart',action='store_true',help='Start at Linux desktop login (requires a fixed installation path)')
    parser.add_argument('--uninstall',action='store_true',help='Remove owned runtime and launcher; preserve all user data')
    args=parser.parse_args(argv)
    try:
        if sys.version_info<(3,11): raise ValueError('Python 3.11 or newer is required.')
        if args.skills and args.portable: raise ValueError('--skills is available only for nonportable installs; portable packages keep their skill inside the release.')
        if args.autostart and args.portable: raise ValueError('Portable installs cannot have fixed-path desktop autostart. Use --start after relocation.')
        prefix=Path(args.prefix).expanduser().resolve()
        mode='portable' if args.portable else ('user' if prefix==Path.home()/'.local' else 'custom prefix')
        print('Mode: '+mode+' · Prefix: '+str(prefix))
        if args.dry_run:
            print('Action: '+('remove owned runtime and launcher; preserve data' if args.uninstall else 'copy immutable runtime, install launcher, preserve existing data'))
            print('Launcher: '+str(prefix/'bin'/APP))
            print('Runtime: '+str(prefix/'share'/APP/'releases'))
            print('Data: '+(str(prefix/'var'/APP) if args.portable else (args.data or 'preserve configured location; otherwise ~/.local/share/conversation-foundry/data')))
            if args.skills: print('Skills: '+(', '.join(str(path) for _,path in agent_skill_targets()) or 'no existing supported agent directories'))
            print('Service: '+('restart only the authenticated service for this data directory' if args.start else 'leave service unchanged'))
            return 0
        if args.uninstall:
            uninstall(args.prefix); print('Uninstalled runtime and launcher. Data and backups were preserved.'); return 0
        launcher,config=install(args.prefix,portable=args.portable,data=args.data)
        print('Installed '+config['release']); print('Launcher: '+str(launcher))
        print('Data: '+str(launcher.parent.parent/'var'/APP if config['portable'] else config['data']))
        print('Start: '+str(launcher)+' start --background')
        print('Status: '+str(launcher)+' status')
        if sys.platform.startswith('linux') and prefix==Path.home()/'.local' and not args.portable:
            print('Applications menu: '+str(desktop_entry(launcher)))
        if args.skills:
            release=prefix/'share'/APP/'releases'/config['release']
            for result in install_skills(prefix,release): print('Skills: '+result)
        if args.autostart: print('Autostart: '+str(autostart(launcher)))
        if args.start: return restart_server(launcher)
        return 0
    except (ValueError,OSError,json.JSONDecodeError) as exc:
        print('Installation error: '+str(exc),file=sys.stderr); return 1

if __name__=='__main__': raise SystemExit(main())
