"""SQLite store (WAL) with append-only evidence tables, a key-value area, and the job queue."""
from __future__ import annotations
import json, os, sqlite3, tempfile, threading
from contextlib import closing, contextmanager
from pathlib import Path
from .common import js, now, uid

DEFAULTS={'capture':True,'auto_tasks':False,'task_idle_seconds':120,'auto_derive':True,'auto_evaluate':False,'auto_promote':False,
          'poll_seconds':15,'provider':{'kind':'pi','command':['pi'],'model':'gpt-6.1-sol','provider':'openai-codex','reasoning':'medium','timeout':180},
          'replay':{'kind':'codex','command':['codex'],'model':'','timeout':180},
          'max_input_chars':160000,'repetitions':3,'max_calls':36,'experiment_seconds':3600,
          'min_quality_gain':0.05,'max_time_ratio':1.25,'min_efficiency_gain':0.15,
          'max_source_bytes':64000000,'max_files_per_scan':200,'max_scan_seconds':10}

SCHEMA='''
CREATE TABLE IF NOT EXISTS kv(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sources(id TEXT PRIMARY KEY,path TEXT UNIQUE NOT NULL,adapter TEXT NOT NULL,enabled INTEGER NOT NULL,created REAL NOT NULL,error TEXT);
CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY,source_id TEXT NOT NULL,path TEXT NOT NULL,title TEXT NOT NULL,revision TEXT NOT NULL,updated REAL NOT NULL,completion TEXT NOT NULL,coverage TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS revisions(id TEXT PRIMARY KEY,session_id TEXT NOT NULL,events TEXT NOT NULL,created REAL NOT NULL);
CREATE TABLE IF NOT EXISTS episodes(id TEXT PRIMARY KEY,session_id TEXT NOT NULL,revision TEXT NOT NULL,title TEXT NOT NULL,first_event INTEGER NOT NULL,last_event INTEGER NOT NULL,label TEXT NOT NULL DEFAULT 'pending',feedback TEXT NOT NULL DEFAULT '',cases_json TEXT NOT NULL DEFAULT '[]',created REAL NOT NULL);
CREATE TABLE IF NOT EXISTS labels(id TEXT PRIMARY KEY,episode_id TEXT NOT NULL,label TEXT NOT NULL,feedback TEXT NOT NULL,created REAL NOT NULL);
CREATE TABLE IF NOT EXISTS task_completions(episode_id TEXT PRIMARY KEY,completed INTEGER NOT NULL,confirmed_at REAL,generation TEXT NOT NULL,updated REAL NOT NULL);
CREATE TABLE IF NOT EXISTS candidates(id TEXT PRIMARY KEY,episode_id TEXT NOT NULL,spec TEXT NOT NULL,status TEXT NOT NULL,base_version TEXT,created REAL NOT NULL);
CREATE TABLE IF NOT EXISTS cases(id TEXT PRIMARY KEY,title TEXT NOT NULL,prompt TEXT NOT NULL,snapshot TEXT NOT NULL,verify_json TEXT NOT NULL,negative_json TEXT NOT NULL,domain TEXT NOT NULL,split TEXT NOT NULL,created REAL NOT NULL);
CREATE TABLE IF NOT EXISTS jobs(id TEXT PRIMARY KEY,kind TEXT NOT NULL,subject TEXT NOT NULL,payload TEXT NOT NULL,status TEXT NOT NULL,error TEXT,progress TEXT NOT NULL,created REAL NOT NULL,updated REAL NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_job ON jobs(kind,subject) WHERE status IN ('queued','running');
CREATE TABLE IF NOT EXISTS experiments(id TEXT PRIMARY KEY,candidate_id TEXT NOT NULL,base_version TEXT,report TEXT NOT NULL,status TEXT NOT NULL,created REAL NOT NULL);
CREATE TABLE IF NOT EXISTS versions(id TEXT PRIMARY KEY,parent TEXT,members TEXT NOT NULL,created REAL NOT NULL,reason TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS audit(id TEXT PRIMARY KEY,action TEXT NOT NULL,detail TEXT NOT NULL,created REAL NOT NULL);
CREATE TABLE IF NOT EXISTS file_marks(path TEXT PRIMARY KEY,fingerprint TEXT NOT NULL,seen REAL NOT NULL);
CREATE TABLE IF NOT EXISTS decisions(id TEXT PRIMARY KEY,candidate_id TEXT NOT NULL,action TEXT NOT NULL,comment TEXT NOT NULL,detail TEXT NOT NULL,created REAL NOT NULL);
CREATE TRIGGER IF NOT EXISTS decisions_no_update BEFORE UPDATE ON decisions BEGIN SELECT RAISE(ABORT,'decisions is append-only'); END;
CREATE TRIGGER IF NOT EXISTS decisions_no_delete BEFORE DELETE ON decisions BEGIN SELECT RAISE(ABORT,'decisions is append-only'); END;
'''

class Store:
    def __init__(self,root):
        self.root=Path(root).expanduser().resolve(); self.root.mkdir(parents=True,exist_ok=True,mode=0o700)
        self.lock=threading.RLock()
        self.db=sqlite3.connect(self.root/'foundry.sqlite',timeout=30,check_same_thread=False,isolation_level=None)
        self.db.row_factory=sqlite3.Row
        self.db.executescript('PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON;'+SCHEMA)
        if not self.get('config'): self.set('config',DEFAULTS)
        with self.transaction():
            cfg=self.get('config')
            # Add new settings without replacing a user's existing agent or model.
            merged={**DEFAULTS,**cfg}
            if merged!=cfg:
                cfg=merged; self.set('config',cfg)
            if cfg.get('auto_promote') is not False:
                cfg['auto_promote']=False; self.set('config',cfg)
                self.audit('automatic_promotion_disabled',{'reason':'human_approval_required'})
            if not self.get('human_completion_migration_v1'):
                historical=self.rows("SELECT id,created FROM episodes WHERE label IN ('correct','incorrect')")
                for episode in historical:
                    label=self.one('SELECT MAX(created) AS at FROM labels WHERE episode_id=?',(episode['id'],))
                    at=label['at'] if label and label['at'] is not None else episode['created']
                    self.execute('INSERT OR IGNORE INTO task_completions VALUES (?,?,?,?,?)',
                                 (episode['id'],1,at,uid('completion'),now()))
                self.set('human_completion_migration_v1',True)
                self.audit('human_completion_migrated',{'historical_labels_acknowledged':len(historical)})
        if not self.get('token'): self.set('token',os.urandom(32).hex())
        if not self.get('active_version'):
            self.db.execute('INSERT OR IGNORE INTO versions VALUES (?,?,?,?,?)',('initial',None,'[]',now(),'Arnés inicial'))
            self.set('active_version','initial')
        for p in ('snapshots','runs','registry','exports','backups'): (self.root/p).mkdir(exist_ok=True)
        try: os.chmod(self.root/'foundry.sqlite',0o600)
        except OSError: pass
    def rows(self,sql,args=()):
        with self.lock: return [dict(r) for r in self.db.execute(sql,args)]
    def one(self,sql,args=()):
        # Avoid materializing an entire result when the caller needs one row.
        with self.lock, closing(self.db.execute(sql,args)) as cursor:
            row=cursor.fetchone()
            return dict(row) if row is not None else None
    def execute(self,sql,args=()):
        with self.lock: return self.db.execute(sql,args)
    @contextmanager
    def transaction(self):
        with self.lock:
            self.db.execute('BEGIN IMMEDIATE')
            try: yield; self.db.execute('COMMIT')
            except BaseException: self.db.execute('ROLLBACK'); raise
    def get(self,key,default=None):
        row=self.one('SELECT value FROM kv WHERE key=?',(key,)); return json.loads(row['value']) if row else default
    def set(self,key,value): self.execute('INSERT OR REPLACE INTO kv VALUES (?,?)',(key,js(value)))
    def audit(self,action,detail): self.execute('INSERT INTO audit VALUES (?,?,?,?)',(uid('event'),action,js(detail),now()))
    def task_completion(self,episode_id):
        row=self.one('SELECT * FROM task_completions WHERE episode_id=?',(episode_id,))
        return {'completed':bool(row and row['completed']),
                'completion_confirmed_at':row['confirmed_at'] if row and row['completed'] else None,
                'completion_generation':row['generation'] if row else None}
    def task_completed(self,episode_id): return self.task_completion(episode_id)['completed']
    def enrich_episode(self,row):
        return {**row,**self.task_completion(row['id']),'origin':self.get('episode_origin:'+row['id']),
                'proposal':self.get('task_proposal:'+row['id']),'superseded':self.get('task_superseded:'+row['id'])}
    def job(self,kind,subject,payload=None):
        with self.transaction():
            old=self.one("SELECT id FROM jobs WHERE kind=? AND subject=? AND status IN ('queued','running')",(kind,subject))
            if old: return old['id']
            id=uid('job'); self.execute('INSERT INTO jobs VALUES (?,?,?,?,?,?,?,?,?)',
                (id,kind,subject,js(payload or {}),'queued',None,'En cola',now(),now()))
            return id
    def progress(self,id,text): self.execute('UPDATE jobs SET progress=?,updated=? WHERE id=?',(text,now(),id))
    def cancelled(self,id):
        r=self.one('SELECT status FROM jobs WHERE id=?',(id,)); return not r or r['status']=='cancelled'
    def claim(self):
        with self.transaction():
            row=self.one("SELECT * FROM jobs WHERE status='queued' ORDER BY created LIMIT 1")
            if row: self.execute("UPDATE jobs SET status='running',updated=? WHERE id=?",(now(),row['id']))
            return row
    def recover(self):
        # A completed external call cannot safely be assumed unexecuted after a crash.
        self.execute("UPDATE jobs SET status='interrupted',error='El proceso se reinició. Reintenta para continuar con checkpoints.',updated=? WHERE status='running'",(now(),))
    def backup(self):
        # Allocate exclusively: two requests in one second must retain both
        # snapshots. mkstemp also creates the database with private permissions.
        fd,name=tempfile.mkstemp(prefix='foundry-'+str(int(now()))+'-',suffix='.sqlite',dir=self.root/'backups')
        os.close(fd);path=Path(name)
        try:
            with self.lock, closing(sqlite3.connect(path)) as dest:
                self.db.backup(dest)
        except BaseException:
            path.unlink(missing_ok=True)
            raise
        return str(path)
