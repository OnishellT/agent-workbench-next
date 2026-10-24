"""Real startup failures must not retain sockets or database handles."""
from pathlib import Path
import socket
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

from foundry_loop.app import App
from foundry_loop.server import serve


class ServerStartupTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory()
        self.app=App(Path(self.temp.name))

    def tearDown(self):
        self.app.store.db.close()
        self.temp.cleanup()

    def assert_closed_without_workers(self):
        with self.assertRaises(sqlite3.ProgrammingError):self.app.store.get('config')
        self.assertIsNone(self.app.thread)
        self.assertIsNone(self.app.capture_thread)

    def test_occupied_port_closes_the_unused_database(self):
        with socket.socket() as occupied:
            occupied.bind(('127.0.0.1',0));occupied.listen()
            with patch('foundry_loop.server.App',return_value=self.app):
                with self.assertRaises(OSError):serve(self.app.store.root,occupied.getsockname()[1])
        self.assert_closed_without_workers()

    def test_ready_callback_failure_closes_socket_and_database(self):
        ports=[]
        def ready(metadata):
            ports.append(metadata['port'])
            raise RuntimeError('authored startup failure')
        with patch('foundry_loop.server.App',return_value=self.app):
            with self.assertRaisesRegex(RuntimeError,'authored startup failure'):
                serve(self.app.store.root,0,ready)
        self.assert_closed_without_workers()
        with socket.socket() as replacement:
            replacement.bind(('127.0.0.1',ports[0]))
