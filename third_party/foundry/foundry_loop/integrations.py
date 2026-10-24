"""Consume validated registry versions in live projects without overwriting user work."""
from __future__ import annotations
import hashlib
import json
import os
from pathlib import Path
import re
import tempfile

from .common import Invalid, clean, digest, js, safe_relative
from .evaluate import _members, _candidate, _applicable

BEGIN = b'\n<!-- FOUNDRY:BEGIN managed active capabilities -->\n'
END = b'<!-- FOUNDRY:END managed active capabilities -->\n'
MANIFEST = '.foundry-sync.json'


def _hash(data):
    return hashlib.sha256(data).hexdigest()


def _checked_path(root, relative):
    path = root / relative
    try:
        path.relative_to(root)
    except ValueError:
        raise Invalid('La ruta sale del proyecto.') from None
    if '..' in Path(relative).parts or Path(relative).is_absolute():
        raise Invalid('Ruta de sincronización inválida.')
    cursor = root
    for part in Path(relative).parts:
        cursor = cursor / part
        if cursor.is_symlink():
            raise Invalid('No se sincronizan rutas con enlaces simbólicos.')
    return path


def active_context(store, domain, project=None):
    if not isinstance(domain, str) or not domain.strip():
        raise Invalid('Indica el dominio de la tarea.')
    domain = clean(domain).strip()
    project_path = Path(project).expanduser().resolve() if project else None
    version = store.get('active_version')
    if not re.fullmatch(r'(?:initial|version_[a-f0-9]+)', str(version)):
        raise Invalid('Versión activa inválida.')
    members = _members(store, version)
    result = {'version': version, 'domain': domain, 'project': str(project_path) if project_path else None,
              'instructions': '', 'capabilities': [], 'package_hash': None, 'warnings': []}
    if version == 'initial':
        if members:
            raise Invalid('La versión inicial no puede contener capacidades sin evaluar.')
        return result
    folder = _checked_path(store.root, 'registry/' + version)
    manifest_path = _checked_path(folder, 'manifest.json')
    try:
        package = json.loads(manifest_path.read_text(encoding='utf8'))
    except (ValueError, OSError):
        raise Invalid('El paquete activo no es legible.') from None
    package_hash = digest(package)
    accepted = False
    for entry in store.rows("SELECT detail FROM audit WHERE action='registry.promoted'"):
        detail = json.loads(entry['detail'])
        if detail.get('version') == version and detail.get('package_hash') == package_hash:
            accepted = True
            break
    if not accepted or package.get('id') != version:
        raise Invalid('El hash del paquete no coincide con una promoción validada.')
    expected = {(m['candidate_id'], m['variant']) for m in members}
    artifacts = package.get('artifacts', [])
    if not isinstance(artifacts, list) or len(artifacts) != len(expected) or {(a.get('candidate_id'), a.get('variant')) for a in artifacts} != expected:
        raise Invalid('La lista de artefactos del paquete está incompleta o duplicada.')
    project_key = digest(str(project_path)) if project_path else None
    project_key = store.get('project_path_aliases', {}).get(project_key, project_key)
    case = {'domain': domain, 'snapshot': js({'project_key': project_key})}
    for artifact in artifacts:
        candidate, spec = _candidate(store, artifact['candidate_id'])
        if not re.fullmatch(r'candidate_[a-f0-9]+', candidate['id']):
            raise Invalid('Identidad de capacidad inválida.')
        if not re.fullmatch(r'[a-z0-9][a-z0-9-]{0,62}', spec.get('name', '')):
            raise Invalid('Nombre de capacidad inválido.')
        for key in ('name', 'kind', 'scope', 'domain'):
            if artifact.get(key) != spec.get(key):
                raise Invalid('El alcance o metadatos de una capacidad aceptada cambiaron.')
        variant = spec.get(artifact['variant'])
        if variant != artifact.get('content'):
            raise Invalid('Una capacidad aceptada cambió después de la evaluación.')
        files = []
        for file in variant.get('files', []):
            relative = safe_relative(file['path']).as_posix()
            path = _checked_path(folder, 'artifacts/' + candidate['id'] + '/' + relative)
            expected_bytes = file['content'].encode('utf8')
            if not path.is_file() or path.read_bytes() != expected_bytes:
                raise Invalid('Un archivo del registro activo cambió.')
            files.append({'path': str(path), 'relative_path': relative, 'sha256': _hash(expected_bytes)})
        episode = store.one('SELECT * FROM episodes WHERE id=?', (candidate['episode_id'],))
        completion = store.task_completion(candidate['episode_id'])
        binding = digest({k: episode[k] for k in ('revision', 'label', 'feedback', 'first_event', 'last_event')}) if episode else None
        if not episode or not completion['completed'] or episode['label'] not in ('correct', 'incorrect') or spec.get('episode_binding') != binding or spec.get('completion_generation') != completion['completion_generation']:
            result['warnings'].append({'candidate_id': candidate['id'], 'reason': 'La evidencia o etiqueta cambió; capacidad omitida hasta reevaluar.'})
            continue
        if not _applicable(store, candidate, spec, case):
            continue
        result['capabilities'].append({'candidate_id': candidate['id'], 'name': spec['name'], 'kind': spec['kind'],
                                       'scope': spec['scope'], 'domain': spec.get('domain'), 'description': spec.get('description', ''),
                                       'variant': artifact['variant'], 'instructions': variant['instructions'], 'files': files})
    result['package_hash'] = package_hash
    sections = []
    for cap in result['capabilities']:
        text = '## ' + cap['name'] + '\n' + cap['instructions']
        if cap['files']:
            text += '\nArchivos de la capacidad validada:\n' + '\n'.join('- ' + f['path'] for f in cap['files'])
        sections.append(text)
    result['instructions'] = '\n\n'.join(sections)
    return result


def active_catalog(store, project=None):
    """Return a small validated discovery index; load procedure bodies on demand."""
    with store.transaction():
        version = store.get('active_version')
        members = _members(store, version)
        domains = set()
        for member in members:
            _, spec = _candidate(store, member['candidate_id'])
            domain = spec.get('domain')
            if not isinstance(domain, str) or not domain.strip():
                raise Invalid('Una capacidad activa no declara un dominio válido.')
            domains.add(domain)
        summaries = {}; warnings = {}
        # Validate even an empty accepted version; never infer that an empty
        # list allows bypassing the package hash or initial-version checks.
        for domain in sorted(domains) or ['general']:
            context = active_context(store, domain, project)
            for capability in context['capabilities']:
                summaries[capability['candidate_id']] = {key: capability[key] for key in
                    ('candidate_id', 'name', 'domain', 'kind', 'scope', 'description')}
            for warning in context['warnings']:
                warnings[js(warning)] = warning
        capabilities = sorted(summaries.values(), key=lambda item: (item['domain'], item['name'], item['candidate_id']))
        return {'version': version, 'project': str(Path(project).expanduser().resolve()) if project else None,
                'domains': sorted({item['domain'] for item in capabilities}),
                'capabilities': capabilities, 'warnings': list(warnings.values())}


def _write_bytes(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    mode = path.stat().st_mode & 0o777 if path.exists() else 0o644
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as temp:
        name = temp.name
        os.chmod(name, mode)
        temp.write(data); temp.flush(); os.fsync(temp.fileno())
    try:
        os.replace(name, path)
    finally:
        if os.path.exists(name): os.unlink(name)


def _remove_empty(root, relative):
    directory = (root / relative).parent
    while directory != root / '.agents/skills' and directory.is_relative_to(root / '.agents/skills'):
        try: directory.rmdir()
        except OSError: break
        directory = directory.parent


def sync_project(store, project, domain):
    proposed = Path(project).expanduser().absolute()
    if proposed.is_symlink() or not proposed.is_dir():
        raise Invalid('El proyecto debe ser un directorio existente sin enlace simbólico.')
    root = proposed.resolve()
    if root == store.root or root in store.root.parents or store.root in root.parents:
        raise Invalid('El proyecto y el almacén de Foundry deben estar separados.')
    context = active_context(store, domain, root)
    manifest_path = _checked_path(root, MANIFEST)
    old = None
    if manifest_path.exists():
        try:
            old = json.loads(manifest_path.read_text(encoding='utf8'))
        except (OSError, ValueError):
            raise Invalid('El manifiesto previo de sincronización está dañado.') from None
        if not isinstance(old, dict) or old.get('owner') != 'conversation-foundry' or old.get('schema') != 1 or not isinstance(old.get('files'), dict):
            raise Invalid('El manifiesto existente pertenece a otra herramienta.')
    previous = old['files'] if old else {}
    desired = {}
    seen_names = set()
    for cap in context['capabilities']:
        if cap['name'] in seen_names:
            raise Invalid('Dos capacidades activas comparten el mismo nombre de instalación.')
        seen_names.add(cap['name'])
        base = '.agents/skills/foundry-' + cap['name']
        target_dir = _checked_path(root, base)
        if target_dir.exists() and not any(path.startswith(base + '/') for path in previous):
            raise Invalid('La carpeta de la capacidad ya existe y no pertenece a Foundry: ' + base)
        for file in cap['files']:
            relative = base + '/' + file['relative_path']
            if relative in desired:
                raise Invalid('Dos capacidades intentan instalar el mismo archivo.')
            data = Path(file['path']).read_bytes()
            if _hash(data) != file['sha256']:
                raise Invalid('El registro cambió durante la sincronización.')
            desired[relative] = data
        skill = base + '/SKILL.md'
        if skill not in desired:
            text = '---\nname: foundry-' + cap['name'] + '\ndescription: ' + js(cap['description'] or cap['name']) + '\n---\n\n' + cap['instructions'] + '\n'
            desired[skill] = text.encode('utf8')
    # Validate every old and new target before changing any bytes.
    for relative, expected_hash in previous.items():
        if not isinstance(relative, str) or not relative.startswith('.agents/skills/foundry-'):
            raise Invalid('El manifiesto contiene una ruta no administrada.')
        path = _checked_path(root, relative)
        if not path.is_file() or _hash(path.read_bytes()) != expected_hash:
            raise Invalid('Un archivo administrado fue editado o eliminado; conserva tus cambios antes de sincronizar: ' + relative)
    for relative in desired:
        path = _checked_path(root, relative)
        if path.exists() and relative not in previous:
            raise Invalid('Foundry no sobrescribe archivos ajenos: ' + relative)
    agents = _checked_path(root, 'AGENTS.md')
    before_agents = agents.read_bytes() if agents.exists() else b''
    has_markers = b'<!-- FOUNDRY:' in before_agents
    prior_block = None
    if old and old.get('block_sha256'):
        if before_agents.count(BEGIN) != 1 or before_agents.count(END) != 1:
            raise Invalid('El bloque administrado de AGENTS.md falta o está duplicado.')
        left = before_agents.index(BEGIN); right = before_agents.index(END, left) + len(END)
        prior_block = before_agents[left:right]
        if _hash(prior_block) != old['block_sha256']:
            raise Invalid('El bloque administrado de AGENTS.md fue editado; no se sobrescribirá.')
    elif has_markers:
        raise Invalid('AGENTS.md contiene un bloque Foundry de propietario desconocido.')
    parts = ['Capacidades evaluadas de Conversation Foundry. Versión: ' + context['version'] + '.']
    for cap in context['capabilities']:
        skill_path = '.agents/skills/foundry-' + cap['name'] + '/SKILL.md'
        parts.append('- ' + cap['name'] + ': consulta `' + skill_path + '` cuando corresponda al dominio ' + str(cap['domain']) + '.')
        if cap['kind'] == 'rule':
            parts.append(cap['instructions'])
    block = BEGIN + ('\n\n'.join(parts) + '\n').encode('utf8') + END if context['capabilities'] else b''
    if block and (len(block) > 65536 or block.count(BEGIN) > 1 or block.count(END) > 1 or b'<!-- FOUNDRY:' in block[len(BEGIN):-len(END)]):
        raise Invalid('El bloque de instrucciones es demasiado grande o contiene marcadores reservados.')
    if prior_block:
        after_agents = before_agents.replace(prior_block, block, 1)
    else:
        after_agents = before_agents + block
    new_manifest = {'schema': 1, 'owner': 'conversation-foundry', 'version': context['version'], 'domain': context['domain'],
                    'package_hash': context['package_hash'], 'files': {p: _hash(data) for p, data in desired.items()},
                    'block_sha256': _hash(block) if block else None}
    changes = {relative: data for relative, data in desired.items()}
    changes.update({relative: None for relative in previous if relative not in desired})
    if after_agents != before_agents:
        changes['AGENTS.md'] = after_agents
    changes[MANIFEST] = (js(new_manifest) + '\n').encode('utf8')
    with store.transaction():
        if digest(active_context(store, domain, root)) != digest(context):
            raise Invalid('El arnés o sus etiquetas cambiaron durante la sincronización; vuelve a intentarlo.')
        originals = {relative: (_checked_path(root, relative).read_bytes() if _checked_path(root, relative).is_file() else None) for relative in changes}
        applied = []
        try:
            for relative, data in changes.items():
                path = _checked_path(root, relative)
                if data is None:
                    path.unlink()
                else:
                    _write_bytes(path, data)
                applied.append(relative)
        except BaseException:
            for relative in reversed(applied):
                path = _checked_path(root, relative)
                data = originals[relative]
                if data is None:
                    if path.exists(): path.unlink()
                else: _write_bytes(path, data)
            for relative in desired:
                _remove_empty(root, relative)
            raise
        for relative in previous:
            _remove_empty(root, relative)
        store.audit('project.synced', {'project_key': digest(str(root)), 'version': context['version'], 'domain': domain, 'files': len(desired), 'manifest_hash': digest(new_manifest)})
    return {'version': context['version'], 'project': str(root), 'domain': context['domain'], 'installed_files': len(desired),
            'removed_files': len(set(previous) - set(desired)), 'manifest': str(manifest_path), 'capabilities': [c['name'] for c in context['capabilities']], 'warnings': context['warnings']}
