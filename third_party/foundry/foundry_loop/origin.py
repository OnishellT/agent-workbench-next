"""Bind a task to an initial evaluation state, with explicit provenance."""
from __future__ import annotations

import json
from .common import Invalid, clean, digest, now
from .snapshots import case_paths


def confirm_origin(store, episode_id, case_id, confirmed_initial_state, note=''):
    if confirmed_initial_state is not True:
        raise Invalid('Confirma expresamente que el caso conserva el estado inicial de esta tarea.')
    if not isinstance(note,str) or not note.strip():
        raise Invalid('Explica cómo identificaste el estado inicial (por ejemplo, commit o snapshot previo).')
    with store.transaction():
        episode=store.one('SELECT * FROM episodes WHERE id=?',(episode_id,))
        case=store.one("SELECT * FROM cases WHERE id=? AND split='origin'",(case_id,))
        if not episode or not case or case_id not in json.loads(episode['cases_json']):
            raise Invalid('Selecciona un caso de origen vinculado a esta tarea.')
        case_paths(store,case)
        manifest=json.loads(case['snapshot'])
        binding={'source_revision':episode['revision'],'case_id':case_id,
                 'project_hash':manifest['project_hash'],'project_key':manifest['project_key'],
                 'prompt_hash':digest(case['prompt']),
                 'provider_config_hash':digest(store.get('config')['replay']),
                 'captured_before_task':True,'provenance':'human_attested',
                 'note':clean(note.strip()),'confirmed_at':now()}
        store.set('episode_origin:'+episode_id,binding)
        store.audit('episode.origin_confirmed',{'episode_id':episode_id,**binding})
        return binding
