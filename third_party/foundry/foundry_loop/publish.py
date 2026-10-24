"""Export and import of candidates as inspectable Agent Skills packages.

Two review steps are borrowed from hqhq1025/skill-optimizer (MIT; skill-miner /
skill-personalizer / skill-generalizer split): *generalize* strips this
machine's private context before anything leaves the store, and *personalize*
fits an incoming package to the target machine's paths and tools. Both are
deterministic, stdlib-only text passes whose every substitution is written to
review.json so a human can inspect them. Export is never approval: the package
carries the candidate's gate status and risk findings, and an imported skill
is installed as an unmanaged folder that only the local gate could ever promote.
"""
from __future__ import annotations
import getpass, hashlib, json, re, shutil, socket, zipfile
from pathlib import Path
from .common import Invalid, clean, digest, js, now, safe_relative

PRIVATE_FIELDS = ('analysis_coverage', 'provider_evidence', 'similar', 'fingerprint', 'episode_binding', 'completion_generation', 'miner')
TOOL_RE = re.compile(r'(?m)(?:^\s*\$\s+|`)([a-zA-Z][\w.+-]*)(?=[\s`])')
SKIP_TOOLS = {'cd', 'export', 'echo', 'set', 'source', 'if', 'then', 'else', 'fi', 'for', 'do', 'done', 'true', 'false', 'python', 'python3'}


def _identity(store, spec):
    """Values that identify this machine or user; none of them belong in a shared skill."""
    from .readiness import source_directories
    values = []
    revision = spec.get('source_revision')
    for cwd in (source_directories(store, revision) if revision else []):
        values.append(('{project}', str(Path(cwd).resolve())))
    values.append(('~', str(Path.home())))
    try: user = getpass.getuser()
    except Exception: user = ''
    if user and len(user) >= 3: values.append(('{user}', user))
    host = socket.gethostname()
    if host and len(host) >= 3: values.append(('{host}', host))
    return sorted(values, key=lambda item: -len(item[1]))


def generalize_text(text, identity):
    substitutions = []
    for marker, value in identity:
        count = text.count(value)
        if count:
            text = text.replace(value, marker); substitutions.append({'marker': marker, 'occurrences': count})
    cleaned = clean(text)
    if cleaned != text: substitutions.append({'marker': '[REDACTED]', 'occurrences': 'redaction'})
    return cleaned, substitutions


def personalize_text(text, project):
    home = str(Path.home()); project = str(Path(project).expanduser().resolve())
    substitutions = []
    for marker, value in (('{project}', project), ('~/', home + '/'), ('{user}', getpass.getuser()), ('{host}', socket.gethostname())):
        count = text.count(marker)
        if count:
            text = text.replace(marker, value); substitutions.append({'marker': marker, 'occurrences': count})
    tools = sorted({m.group(1) for m in TOOL_RE.finditer(text)} - SKIP_TOOLS)
    return text, {'substitutions': substitutions, 'tools_required': tools, 'tools_missing_here': [t for t in tools if not shutil.which(t)]}


def skill_markdown(name, description, instructions, files=()):
    text = '---\nname: ' + name + '\ndescription: ' + js(description) + '\n---\n\n' + instructions.rstrip() + '\n'
    if files: text += '\nFiles shipped with this skill:\n' + ''.join('- ' + f + '\n' for f in files)
    return text


def export_candidate(store, candidate_id):
    row = store.one('SELECT * FROM candidates WHERE id=?', (candidate_id,))
    if not row: raise Invalid('Aprendizaje desconocido.')
    from .miner import risk_acknowledged
    spec = json.loads(row['spec']); identity = _identity(store, spec)
    public = {k: v for k, v in spec.items() if k not in PRIVATE_FIELDS}
    review = {'generalize': {'removed_fields': [k for k in spec if k in PRIVATE_FIELDS], 'substitutions': {}},
              'personalize': {'tools_required': [], 'note': 'Run conversation-foundry import on the target machine; it substitutes {project}/~ and lists missing tools.'}}
    # Evidence quotes are transcript excerpts: keep only their shape outside the store.
    public['evidence'] = [{'event_index': e['event_index'], 'quote_sha256': hashlib.sha256(e['quote'].encode()).hexdigest()} for e in spec.get('evidence', [])]
    for key in ('positive', 'preventive'):
        variant = spec.get(key) or {'instructions': '', 'files': []}
        instructions, subs = generalize_text(variant.get('instructions', ''), identity)
        files = []
        for file in variant.get('files', []):
            content, file_subs = generalize_text(file['content'], identity)
            files.append({'path': str(safe_relative(file['path'])), 'content': content}); subs += file_subs
        public[key] = {'instructions': instructions, 'files': files}
        review['generalize']['substitutions'][key] = subs
        tools = sorted({m.group(1) for m in TOOL_RE.finditer(instructions)} - SKIP_TOOLS)
        review['personalize']['tools_required'] = sorted(set(review['personalize']['tools_required']) | set(tools))
    for key in ('description', 'rationale', 'title'):
        if isinstance(public.get(key), str): public[key], _ = generalize_text(public[key], identity)
    variant = public.get(spec.get('preferred_variant', 'positive')) or public['positive']
    skill_text = skill_markdown(public['name'], public.get('description', ''), variant['instructions'], [f['path'] for f in variant['files']])
    manifest = {'schema': 2, 'candidate_id': candidate_id, 'status': row['status'], 'exported_at': now(),
                'gate': {'status': row['status'], 'adopted': row['status'] == 'accepted', 'export_is_not_approval': True},
                'risk': spec.get('risk') or [], 'risk_acknowledged': risk_acknowledged(store, candidate_id, spec),
                'variant': spec.get('preferred_variant', 'positive'), 'spec': public, 'review': review,
                'files': {'skill/' + public['name'] + '/SKILL.md': hashlib.sha256(skill_text.encode()).hexdigest()}}
    entries = {'skill/' + public['name'] + '/SKILL.md': skill_text}
    for key in ('positive', 'preventive'):
        entries[key + '/instructions.md'] = public[key]['instructions']
        for file in public[key]['files']:
            entries[key + '/' + file['path']] = file['content']
            if key == manifest['variant']: entries['skill/' + public['name'] + '/' + file['path']] = file['content']
    manifest['files'] = {path: hashlib.sha256(text.encode()).hexdigest() for path, text in entries.items()}
    path = store.root / 'exports' / (candidate_id + '.zip')
    with zipfile.ZipFile(path, 'w', zipfile.ZIP_DEFLATED) as z:
        z.writestr('manifest.json', js(manifest)); z.writestr('review.json', js(review))
        for entry, text in entries.items(): z.writestr(entry, text)
    store.audit('candidate.exported', {'candidate_id': candidate_id, 'package_hash': digest(manifest), 'risk': manifest['risk']})
    return {'download': '/api/export?id=' + candidate_id, 'path': str(path), 'review': review, 'risk': manifest['risk'], 'status': row['status']}


def inspect_package(package):
    package = Path(package).expanduser()
    if not package.is_file(): raise Invalid('El paquete no existe.')
    with zipfile.ZipFile(package) as z:
        names = z.namelist()
        if 'manifest.json' not in names: raise Invalid('El paquete no contiene manifest.json.')
        try: manifest = json.loads(z.read('manifest.json').decode('utf8'))
        except ValueError: raise Invalid('manifest.json inválido.') from None
        if not isinstance(manifest, dict) or manifest.get('schema') != 2 or not isinstance(manifest.get('files'), dict): raise Invalid('El manifiesto no es un paquete de Foundry (schema 2).')
        contents = {}
        for entry, expected in manifest['files'].items():
            safe_relative(entry)
            if entry not in names: raise Invalid('Falta un archivo declarado: ' + entry)
            data = z.read(entry)
            if hashlib.sha256(data).hexdigest() != expected: raise Invalid('Un archivo del paquete no coincide con su hash: ' + entry)
            contents[entry] = data.decode('utf8')
        for entry in names:
            if entry not in manifest['files'] and entry not in ('manifest.json', 'review.json'): raise Invalid('El paquete contiene un archivo no declarado: ' + entry)
    spec = manifest.get('spec') or {}
    if not re.fullmatch(r'[a-z0-9][a-z0-9-]{0,62}', str(spec.get('name', ''))): raise Invalid('Nombre de skill inválido en el paquete.')
    return manifest, contents


def import_package(store, package, project, acknowledge_risk=False):
    manifest, contents = inspect_package(package)
    root = Path(project).expanduser()
    if root.is_symlink() or not root.is_dir(): raise Invalid('El proyecto debe ser un directorio existente sin enlace simbólico.')
    root = root.resolve()
    if root == store.root or root in store.root.parents or store.root in root.parents: raise Invalid('El proyecto y el almacén de Foundry deben estar separados.')
    if manifest.get('risk') and not (manifest.get('risk_acknowledged') or acknowledge_risk):
        raise Invalid('El paquete tiene hallazgos de riesgo sin reconocer (' + ', '.join(manifest['risk']) + '). Revísalos y vuelve a importar con --acknowledge-risk.')
    name = manifest['spec']['name']; prefix = 'skill/' + name + '/'
    target = root / '.agents' / 'skills' / name
    cursor = root
    for part in ('.agents', 'skills', name):
        cursor = cursor / part
        if cursor.is_symlink(): raise Invalid('No se importa a través de enlaces simbólicos.')
    if target.exists(): raise Invalid('La carpeta de la skill ya existe y no se sobrescribe: ' + str(target))
    written = {}; reports = {}
    staged = {entry[len(prefix):]: text for entry, text in contents.items() if entry.startswith(prefix)}
    if 'SKILL.md' not in staged: raise Invalid('El paquete no contiene skill/<name>/SKILL.md.')
    for relative, text in staged.items():
        personalized, report = personalize_text(text, root)
        written[relative] = personalized; reports[relative] = report
    target.mkdir(parents=True)
    try:
        for relative, text in written.items():
            path = target / safe_relative(relative); path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text, encoding='utf8')
    except BaseException:
        shutil.rmtree(target, ignore_errors=True); raise
    missing = sorted({t for r in reports.values() for t in r['tools_missing_here']})
    store.audit('skill.imported', {'candidate_id': manifest.get('candidate_id'), 'name': name, 'project_key': digest(str(root)),
                                   'gate_status': manifest.get('gate', {}).get('status'), 'tools_missing': missing})
    return {'name': name, 'path': str(target), 'files': sorted(written), 'gate': manifest.get('gate'), 'risk': manifest.get('risk', []),
            'personalize': reports, 'tools_missing_here': missing,
            'note': 'Instalada como skill no administrada; su estado de gate proviene de otro almacén y no cuenta como adopción local.'}
