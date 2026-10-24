"""Database behavior under repeated backups and single-row reads."""
from contextlib import closing
import json
import os
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

from foundry_loop.store import Store


class StoreIntegrityTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory()
        self.store=Store(Path(self.temp.name))

    def tearDown(self):
        self.store.db.close()
        self.temp.cleanup()

    def test_backups_in_same_second_preserve_distinct_database_states(self):
        with patch('foundry_loop.store.now',return_value=1700000000):
            self.store.set('authored_state',{'revision':1})
            first=Path(self.store.backup())
            self.store.set('authored_state',{'revision':2})
            second=Path(self.store.backup())
        self.assertNotEqual(first,second)
        for path,expected in ((first,{'revision':1}),(second,{'revision':2})):
            with closing(sqlite3.connect(path)) as backup:
                self.assertEqual(json.loads(backup.execute("SELECT value FROM kv WHERE key='authored_state'").fetchone()[0]),expected)
                self.assertEqual(backup.execute('PRAGMA integrity_check').fetchone()[0],'ok')
            if os.name=='posix':self.assertEqual(path.stat().st_mode & 0o777,0o600)

    def test_backup_destination_is_closed_and_failed_copy_is_removed(self):
        connections=[]
        original_connect=sqlite3.connect
        def connect(*args,**kwargs):
            connection=original_connect(*args,**kwargs);connections.append(connection)
            return connection
        with patch('foundry_loop.store.sqlite3.connect',side_effect=connect):
            saved=Path(self.store.backup())
        self.assertTrue(saved.is_file())
        with self.assertRaises(sqlite3.ProgrammingError):connections[0].execute('SELECT 1')
        before=set(saved.parent.iterdir())
        with patch('foundry_loop.store.sqlite3.connect',side_effect=sqlite3.OperationalError('authored failure')):
            with self.assertRaises(sqlite3.OperationalError):self.store.backup()
        self.assertEqual(set(saved.parent.iterdir()),before)

    def test_single_row_query_does_not_evaluate_remaining_rows(self):
        self.store.execute('CREATE TABLE authored_rows (value INTEGER)')
        with self.store.transaction():
            self.store.db.executemany('INSERT INTO authored_rows VALUES (?)',((n,) for n in range(5000)))
        visited=[]
        def observe(value):visited.append(value);return value
        self.store.db.create_function('observe',1,observe)
        first=self.store.one('SELECT observe(value) AS value FROM authored_rows')
        self.assertEqual(first,{'value':0})
        # SQLite may step once ahead while fetching a row, but must not consume
        # thousands of rows or invoke their expressions just to return one.
        self.assertLessEqual(len(visited),2)
        self.assertIsNone(self.store.one('SELECT value FROM authored_rows WHERE value=?',(-1,)))
        self.store.execute('DROP TABLE authored_rows')  # no cursor left holding it
