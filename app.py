# Eventlet monkey patch must come first
import datetime
from pathlib import Path
import eventlet
eventlet.monkey_patch()

import signal
import sys
from flask import Flask, render_template, request
from flask_socketio import SocketIO, emit
from flask_cors import CORS
import docker
import os
import uuid
from threading import Lock

# Create Flask app and wrap with app context
app = Flask(__name__, static_folder='static')
app.app_context().push()  # Push an application context

# Configure app
CORS(app, resources={r"/*": {"origins": os.environ.get('HOST', '*')}})
app.config['SECRET_KEY'] = os.urandom(24)
socketio = SocketIO(app, cors_allowed_origins="*", async_mode='eventlet')

class RequestMetadata:
    """Handle IP address detection through various proxies and user agent extraction"""

    # Known proxy headers in order of preference
    PROXY_HEADERS = [
        'CF-Connecting-IP',        # Cloudflare
        'X-Forwarded-For',        # General proxy header
        'X-Real-IP',              # Nginx
        'X-Original-Forwarded-For', # Modified forwarded header
        'Forwarded',              # RFC 7239
        'True-Client-IP',         # Akamai and others
        'X-Client-IP',            # Various proxies
    ]

    @staticmethod
    def get_client_ip(request):
        """
        Extract client IP from request considering various proxy headers
        Returns tuple of (ip_address, source_header)
        """
        # Check Cloudflare and other proxy headers first
        for header in RequestMetadata.PROXY_HEADERS:
            if header in request.headers:
                # Handle X-Forwarded-For chains (take the first IP)
                if header == 'X-Forwarded-For':
                    ips = request.headers[header].split(',')
                    return ips[0].strip(), header
                # Handle RFC 7239 Forwarded header
                elif header == 'Forwarded':
                    parts = request.headers[header].split(';')
                    for part in parts:
                        if part.strip().startswith('for='):
                            # Remove possible IPv6 brackets and port
                            ip = part.split('=')[1].strip('"[]').split(':')[0]
                            return ip, header
                return request.headers[header], header

        # Fall back to direct IP
        return request.remote_addr, 'direct'

    @staticmethod
    def get_user_agent(request):
        """Extract and normalize user agent information"""
        ua = request.headers.get('User-Agent', 'Unknown')
        return ua

    @staticmethod
    def get_request_metadata(request):
        """Get complete request metadata including IP and user agent"""
        ip, ip_source = RequestMetadata.get_client_ip(request)
        user_agent = RequestMetadata.get_user_agent(request)

        return {
            'ip_address': ip,
            'ip_source': ip_source,
            'user_agent': user_agent,
            'headers': dict(request.headers)  # Store all headers for debugging
        }

class TTYLogger:
    def __init__(self):
        self.enabled = os.environ.get('TTY_LOGGING_ENABLED', 'false').lower() == 'true'
        self.log_dir = Path(os.environ.get('TTY_LOG_DIR', './logs'))
        if self.enabled:
            self.log_dir.mkdir(exist_ok=True)

    def create_session_log(self, container_id, user_id, ws_id, request=None):
        if not self.enabled:
            return None

        timestamp = datetime.datetime.now().strftime('%Y%m%d-%H%M%S')
        filename = self.log_dir / f"{timestamp}-{container_id[:12]}.log"

        with open(filename, 'w') as f:
            f.write(f"Session Start: {timestamp}\n")
            f.write(f"Container ID: {container_id}\n")
            f.write(f"User ID: {user_id}\n")
            f.write(f"WebSocket ID: {ws_id}\n")

            if request:
                metadata = RequestMetadata.get_request_metadata(request)
                f.write(f"Origin IP: {metadata['ip_address']} (via {metadata['ip_source']})\n")
                f.write(f"User Agent: {metadata['user_agent']}\n")
                # f.write("Request Headers:\n")
                # for header, value in sorted(metadata['headers'].items()):
                #     f.write(f"{header}: {value}\n")
            else:
                f.write(f"Origin IP: Not available\n")
                f.write(f"User Agent: Not available\n")

            f.write("\n=== Command History ===\n\n")

        return filename

    def clean_terminal_output(self, text, command=None):
        """Remove terminal control sequences and clean up terminal output."""
        cleaned_text = text

        # Remove escape sequences (including cursor movements)
        escaped_sequences = [
            '\x1b[D',  # cursor left
            '\x1b[C',  # cursor right
            '\x1b[K',  # clear line
            '\x1b[A',  # cursor up
            '\x1b[B',  # cursor down
            '\x08',    # backspace
            '\x7f',    # delete
            '\x1b',    # escape
            '\x07',    # bell
            '\a',      # bell (alternative representation)
        ]

        for seq in escaped_sequences:
            cleaned_text = cleaned_text.replace(seq, '')

        # Remove bracketed paste mode sequences
        cleaned_text = cleaned_text.replace('[?2004h', '').replace('[?2004l', '')

        # Handle carriage returns properly
        cleaned_text = cleaned_text.replace('\r', '\n')

        # Remove duplicate command echoes
        if command:
            # Remove all instances of the command
            parts = cleaned_text.split('\n')
            cleaned_parts = []
            for part in parts:
                if not part.strip().startswith(command.strip()):
                    cleaned_parts.append(part)
            cleaned_text = '\n'.join(cleaned_parts)

        # Split into lines and clean each line
        lines = cleaned_text.split('\n')
        cleaned_lines = []
        for line in lines:
            # Skip prompt lines
            if ':~$ ' in line or line.endswith('$ '):
                continue
            # Skip empty lines
            if not line.strip():
                continue
            cleaned_lines.append(line.strip())

        # Join lines and remove extra whitespace
        cleaned_text = '\n'.join(cleaned_lines)
        return cleaned_text.strip()

    def log_command(self, filename, command, output):
        if not self.enabled or not filename:
            print(f"Logging disabled, skipping command: {filename}")
            return

        timestamp = datetime.datetime.now().strftime('%H:%M:%S')
        with open(filename, 'a') as f:
            f.write(f"[{timestamp}] > {command.strip()}\n")
            if output:
                cleaned_output = self.clean_terminal_output(output)
                if cleaned_output.strip():  # Only write if there's non-empty output
                    f.write(f"{cleaned_output.strip()}\n")

    def clean_terminal_output(self, text, command=None):
        """Remove terminal control sequences and clean up terminal output."""
        # Remove escape character (0x1b)
        text = text.replace('\x1b', '')
        # Remove bracketed paste mode sequences
        text = text.replace('[?2004h', '').replace('[?2004l', '')
        # Handle carriage returns properly
        text = text.replace('\r', '\n')

        # Remove the command echo if present
        if command:
            text = text.replace(command, '', 1)

        # Remove prompt patterns (e.g., "container_id:~$ ")
        lines = text.split('\n')
        cleaned_lines = []
        for line in lines:
            # Skip prompt lines
            if ':~$ ' in line:
                continue
            # Skip empty lines
            if not line.strip():
                continue
            # Remove backspace sequences and their target characters
            while '[K' in line:
                idx = line.find('[K')
                if idx > 0:  # If there's a character before [K, remove it too
                    line = line[:idx-1] + line[idx+2:]
                else:
                    line = line[idx+2:]
            cleaned_lines.append(line.strip())

        # Join lines and remove extra whitespace
        text = '\n'.join(cleaned_lines)
        return text.strip()

class TTYController:
    def __init__(self):
        self.client = self._get_docker_client()
        self.sessions = {}
        self.user_sessions = {}
        self.lock = Lock()
        self.logger = TTYLogger()

    def create_session(self, ws_id, user_id, request=None):
        with self.lock:
            if user_id in self.user_sessions:
                old_ws_id = self.user_sessions[user_id]
                self.cleanup_session(old_ws_id)

            try:
                container = self.client.containers.run(
                    'terminal-base:latest',
                    detach=True,
                    tty=True,
                    stdin_open=True,
                    remove=True,
                    user='1000:1000',
                    security_opt=['no-new-privileges:true'],
                    cap_drop=['ALL'],
                    mem_limit='64m',
                    pids_limit=100,
                    read_only=True,
                    tmpfs={
                        '/tmp': 'size=64m,noexec,nosuid',
                        '/home': 'size=64m,exec'
                    },
                    environment={
                        "TERM": "xterm",
                        "PS1": "\\w \\$ ",
                        "HOME": "/home/termuser",
                    }
                )

                exec_create = self.client.api.exec_create(
                    container.id,
                    '/bin/bash -l',
                    stdin=True,
                    tty=True
                )

                exec_socket = self.client.api.exec_start(
                    exec_create['Id'],
                    socket=True,
                    tty=True
                )

                # Pass request object to logger
                log_file = self.logger.create_session_log(
                    container.id,
                    user_id,
                    ws_id,
                    request
                )

                self.sessions[ws_id] = {
                    'container': container,
                    'exec_id': exec_create['Id'],
                    'socket': exec_socket,
                    'user_id': user_id,
                    'log_file': log_file,
                    'current_command': '',
                    'buffer': '',
                    'command_complete': False
                }
                self.user_sessions[user_id] = ws_id

                return container.id

            except Exception as e:
                print(f"Error creating container: {e}")
                if ws_id in self.sessions:
                    self.cleanup_session(ws_id)
                raise

    def write_to_container(self, ws_id, data):
        if ws_id not in self.sessions:
            raise Exception("Session not found")

        session = self.sessions[ws_id]
        try:
            socket = session['socket']
            if not socket:
                raise Exception("Socket not connected")

            # Handle command building
            if '\r' in data or '\n' in data:  # Enter key pressed
                session['command_complete'] = True
                # Store final command
                session['final_command'] = session['current_command']
                session['current_command'] = ''
            elif '\x7f' in data or '\x08' in data:  # Backspace (DEL or BS)
                if session['current_command']:
                    session['current_command'] = session['current_command'][:-1]
            elif data.startswith('\x1b['):  # Arrow keys or other escape sequences
                # Don't add escape sequences to command
                pass
            else:
                session['current_command'] += data

            socket._sock.send(data.encode())
        except Exception as e:
            print(f"Error writing to container: {e}")
            raise

    def read_from_container(self, ws_id):
        if ws_id not in self.sessions:
            return None

        session = self.sessions[ws_id]
        try:
            socket = session['socket']
            if not socket:
                return None

            import select
            readable, _, _ = select.select([socket._sock], [], [], 0.1)
            if not readable:
                return None

            data = socket._sock.recv(4096)
            if not data:
                return None

            output = data.decode('utf-8', errors='replace')
            session['buffer'] += output

            # If command was completed (enter pressed) and we have output
            if session['command_complete'] and session['buffer']:
                # Only log if we have a non-empty command
                if session.get('final_command', '').strip():
                    self.logger.log_command(
                        session['log_file'],
                        session['final_command'],
                        session['buffer']
                    )
                # Reset for next command
                session['final_command'] = ''
                session['buffer'] = ''
                session['command_complete'] = False

            return output
        except Exception as e:
            print(f"Error reading from container: {e}")
            return None

    def cleanup_session(self, ws_id):
        with self.lock:
            if ws_id in self.sessions:
                session = self.sessions[ws_id]
                try:
                    user_id = session.get('user_id')

                    if session.get('socket'):
                        try:
                            session['socket']._sock.close()
                        except:
                            pass

                    if session.get('container'):
                        try:
                            session['container'].stop(timeout=1)
                            session['container'].remove(force=True)
                        except:
                            pass

                    if user_id and user_id in self.user_sessions:
                        del self.user_sessions[user_id]

                except Exception as e:
                    print(f"Error in cleanup: {e}")
                finally:
                    del self.sessions[ws_id]

    def _get_docker_client(self):
        # Try different Docker socket locations
        socket_paths = [
            'unix:///var/run/docker.sock',  # Standard Docker socket
            'unix://' + os.path.expanduser('~') + '/.colima/docker.sock',  # Colima socket
            'unix:///run/podman/podman.sock',  # Podman socket
        ]

        for socket_path in socket_paths:
            try:
                client = docker.DockerClient(base_url=socket_path)
                client.ping()  # Test connection
                print(f"Connected to Docker daemon at {socket_path}")
                return client
            except Exception as e:
                print(f"Failed to connect to {socket_path}: {e}")
                continue

        raise Exception("Could not connect to any Docker socket")

tty_controller = TTYController()

def cleanup_all_containers(signum, frame):
    print("\nCleaning up containers before shutdown...")
    # Copy the session IDs since we'll be modifying the dictionary during iteration
    session_ids = list(tty_controller.sessions.keys())
    for ws_id in session_ids:
        try:
            tty_controller.cleanup_session(ws_id)
        except Exception as e:
            print(f"Error cleaning up session {ws_id}: {e}")
    print("Cleanup complete, shutting down")
    sys.exit(0)

@app.route('/')
def index():
    return render_template('terminal.html')

@socketio.on('connect')
def handle_connect():
    ws_id = str(uuid.uuid4())
    user_id = request.sid
    print(f"New connection: {ws_id} (user: {user_id})")
    emit('session_id', {'id': ws_id, 'user_id': user_id})

@socketio.on('start_session')
def handle_start_session(data):
    ws_id = data['id']
    user_id = request.sid
    try:
        container_id = tty_controller.create_session(ws_id, user_id, request)
        print(f"Created container {container_id} for session {ws_id} (user: {user_id})")

        def read_output(ws_id, user_id):
            with app.app_context():
                while True:
                    try:
                        output = tty_controller.read_from_container(ws_id)
                        if output:
                            socketio.emit('output', {'output': output}, room=user_id)
                    except Exception as e:
                        print(f"Error in read loop: {e}")
                        break
                    socketio.sleep(0.05)

        socketio.start_background_task(read_output, ws_id, user_id)
        emit('container_ready')

    except Exception as e:
        print(f"Error creating session: {e}")
        emit('error', {'error': str(e)})

@socketio.on('input')
def handle_input(data):
    ws_id = data.get('id')
    user_input = data.get('input')

    if not ws_id or not user_input:
        return

    try:
        tty_controller.write_to_container(ws_id, user_input)
    except Exception as e:
        print(f"Error handling input: {e}")
        emit('error', {'error': str(e)})

@socketio.on('disconnect')
def handle_disconnect():
    user_id = request.sid
    print(f"Client disconnected (user: {user_id})")
    if user_id in tty_controller.user_sessions:
        ws_id = tty_controller.user_sessions[user_id]
        tty_controller.cleanup_session(ws_id)

signal.signal(signal.SIGINT, cleanup_all_containers)
signal.signal(signal.SIGTERM, cleanup_all_containers)

if __name__ == '__main__':
    try:
        port = int(os.environ.get('PORT', 5001))
        app.logger.info(f"Server starting on port {port}")

        socketio.run(
            app,
            host='0.0.0.0',
            port=port,
            debug=False,
            use_reloader=False,
            log_output=True
        )
    except Exception as e:
        app.logger.error(f"Server failed to start: {e}")
    finally:
        cleanup_all_containers(None, None)
