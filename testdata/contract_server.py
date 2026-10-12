"""Local HTTP contract server for every SDK; never sends email or needs credentials."""
import json
import os
import ssl
import tempfile
import socket
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit, parse_qs

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        self.handle_request()

    def do_POST(self):
        self.handle_request()

    def handle_request(self):
        url = urlsplit(self.path)
        segments = url.path.split('/')
        case = segments[1]
        with self.server.lock:
            self.server.counts[case] = self.server.counts.get(case, 0) + 1
        failures = []
        expected_method = 'GET' if case in ('get', 'list', 'empty-list') else 'POST'
        if self.command != expected_method:
            failures.append('method: ' + case)
        if case != 'get' and url.path != '/' + case + '/v1/emails':
            failures.append('endpoint: ' + url.path)
        if self.headers.get('Authorization') != 'Bearer rr_test_contract':
            failures.append('authorization')
        if self.headers.get('Content-Type', '').split(';')[0] != 'application/json':
            failures.append('content type')
        raw = self.rfile.read(int(self.headers.get('Content-Length', '0')))
        if self.command == 'POST':
            body = json.loads(raw)
            if case == 'send':
                if body != {'from': 'sender@example.com', 'to': ['a@example.com', 'b@example.com'],
                            'subject': 'Hello ☃', 'text': 'line one\nline two',
                            'metadata': {'number': 3, 'enabled': True}, 'tags': {'kind': 'welcome'}}:
                    failures.append('send body')
                if self.headers.get('Idempotency-Key') != 'retry-123':
                    failures.append('idempotency')
            if case == 'template' and body != {'from': 'sender@example.com', 'to': 'a@example.com',
                                              'template': 'welcome', 'variables': {'name': 'Zoë', 'count': 2, 'active': True}}:
                failures.append('template body')
        if case == 'get' and url.path != '/get/v1/emails/email%2Fid%20%2B%3F':
            failures.append('path encoding: ' + url.path)
        if case == 'list' and parse_qs(url.query) != {'status': ['queued'], 'recipient': ['a+tag@example.com'], 'cursor': ['cur /?+']}:
            failures.append('query encoding')
        if case == 'empty-list' and url.query:
            failures.append('empty filters')
        if case not in ('send',) and self.headers.get('Idempotency-Key'):
            failures.append('idempotency leaked')
        if self.command == 'GET' and raw:
            failures.append('GET body')
        if failures:
            with self.server.lock:
                self.server.failures.extend(failures)
        status = 202 if self.command == 'POST' else 200
        result = {'id': 'email_123', 'status': 'queued'}
        if case == 'suppressed': result = {'id': 'email_123', 'status': 'suppressed', 'reason': 'recipient_suppressed'}
        if case == 'simulated': result = {'id': 'email_123', 'status': 'queued', 'test_mode': True}
        if case == 'get': result = {'id': 'email_123', 'to': ['a@example.com'], 'events': [], 'metadata': {}}
        if case in ('list', 'empty-list'): result = {'emails': [], 'next_cursor': None}
        if case in ('unauthorized', 'validation', 'rate', 'server-error'):
            status, code = {'unauthorized': (401, 'invalid_api_key'), 'validation': (422, 'validation_error'),
                            'rate': (429, 'rate_limit_exceeded'), 'server-error': (500, 'internal_error')}[case]
            result = {'error': {'code': code, 'message': 'Contract failure'}}
        if case == 'disconnect':
            self.connection.shutdown(socket.SHUT_RDWR)
            self.connection.close()
            return
        if case == 'bad-fields': status, result = 503, {'error': {'code': 123, 'message': False}}
        if case == 'unknown-error': status, result = 503, {'unexpected': True}
        if case == 'bad-error': status, result = 502, {'error': 'invalid shape'}
        if case == 'redirect': status = 307
        if case == 'timeout': time.sleep(.6)
        content = json.dumps(result).encode()
        if case in ('malformed', 'non-json-error'): content = b'<html>not JSON</html>'
        if case == 'non-json-error': status = 502
        if case == 'array': content = b'[]'
        if case == 'null': content = b'null'
        if case == 'deep-json': content = b'{"nested":' * 80 + b'0' + b'}' * 80
        if case == 'invalid-utf8': content = b'{"invalid":"\xff"}'
        if case == 'oversized': content = b'x' * (16 * 1024 * 1024 + 1)
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(content) + (10 if case == 'truncated' else 5 if case == 'drip-body' else 0)))
        if case == 'redirect': self.send_header('Location', self.server.base_url + '/leak/v1/emails')
        self.end_headers()
        if case == 'slow-body': time.sleep(.6)
        try:
            if case == 'drip-body':
                for _ in range(5):
                    self.wfile.write(b' '); self.wfile.flush(); time.sleep(.08)
            self.wfile.write(content)
        except (BrokenPipeError, ConnectionResetError): pass


def main():
    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    server.daemon_threads = True
    server.counts, server.failures, server.lock = {}, [], threading.Lock()
    server.base_url = 'http://127.0.0.1:' + str(server.server_port)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    tls_files = tempfile.TemporaryDirectory(prefix='raven-tls-')
    cert, key = str(Path(tls_files.name) / 'cert.pem'), str(Path(tls_files.name) / 'key.pem')
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                    '-subj', '/CN=localhost', '-keyout', key, '-out', cert],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    tls = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    tls.daemon_threads = True
    tls.counts, tls.failures, tls.lock = server.counts, server.failures, server.lock
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(cert, key)
    tls.socket = context.wrap_socket(tls.socket, server_side=True)
    threading.Thread(target=tls.serve_forever, daemon=True).start()
    test_env = dict(os.environ, RAVENRELAY_TLS_TEST_URL='https://127.0.0.1:' + str(tls.server_port) + '/tls')
    try:
        result = subprocess.run(sys.argv[1:] + [server.base_url, str(Path(__file__).with_name('webhook-vectors.json'))], env=test_env)
        required = ['send', 'template', 'suppressed', 'simulated', 'get', 'list', 'empty-list',
                    'unauthorized', 'validation', 'rate', 'server-error', 'unknown-error', 'bad-error',
                    'redirect', 'timeout', 'malformed', 'array', 'non-json-error',
                    'null', 'oversized', 'truncated', 'disconnect', 'slow-body', 'bad-fields', 'deep-json', 'invalid-utf8', 'drip-body']
        missing = [case for case in required if server.counts.get(case) != 1]
        if missing: server.failures.append('Expected exactly one request per case: ' + repr(missing))
        if server.counts.get('leak'): server.failures.append('Redirect followed; credentials leaked')
        for forbidden in ('invalid', 'canceled', 'tls'):
            if server.counts.get(forbidden): server.failures.append('Invalid/canceled request reached transport: ' + forbidden)
        if server.failures: raise AssertionError(server.failures)
        if result.returncode: raise SystemExit(result.returncode)
        print('HTTP contract: 27 scenarios passed; no retries or redirect leaks.')
    finally:
        tls.shutdown()
        tls.server_close()
        tls_files.cleanup()
        server.shutdown()
        server.server_close()

if __name__ == '__main__': main()
