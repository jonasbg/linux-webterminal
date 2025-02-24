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
import logging
from logging.handlers import RotatingFileHandler

# Create Flask app and wrap with app context
app = Flask(__name__, static_folder='static')
app.app_context().push()  # Push an application context

# Configure logging for the application
def setup_logging():
    """Configure logging for the application"""
    log_format = '%(asctime)s - %(levelname)s - %(message)s'
    logging.basicConfig(
        level=logging.INFO,
        format=log_format,
        handlers=[
            logging.StreamHandler()
        ]
    )
    # Set Flask logger to use the same configuration
    app.logger.handlers = logging.getLogger().handlers
    app.logger.setLevel(logging.INFO)

setup_logging()
logger = logging.getLogger(__name__)

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
        """Remove terminal control sequences while preserving cursor movement."""
        # Don't modify cursor movement sequences
        preserved_sequences = [
            '\x1b[D',  # cursor left
            '\x1b[C',  # cursor right
            '\x1b[A',  # cursor up
            '\x1b[B',  # cursor down
        ]

        # Only clean non-cursor sequences
        cleaned_text = text
        for seq in [
            '[?2004h',  # bracketed paste mode on
            '[?2004l',  # bracketed paste mode off
            '\x07',    # bell
            '\a',      # bell (alternative)
        ]:
            cleaned_text = cleaned_text.replace(seq, '')

        # Handle command echo cleanup if needed
        if command:
            lines = cleaned_text.split('\n')
            cleaned_lines = []
            for line in lines:
                if not line.strip().startswith(command.strip()):
                    cleaned_lines.append(line)
            cleaned_text = '\n'.join(cleaned_lines)

        # Preserve cursor movement sequences
        final_text = ''
        i = 0
        while i < len(cleaned_text):
            found_preserved = False
            for seq in preserved_sequences:
                if cleaned_text[i:].startswith(seq):
                    final_text += seq
                    i += len(seq)
                    found_preserved = True
                    break
            if not found_preserved:
                final_text += cleaned_text[i]
                i += 1

        return final_text

    def log_command(self, filename, command, output):
        if not self.enabled or not filename:
            print(f"Logging disabled, skipping command: {filename}")
            return
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
        # Get limits from environment
        self.max_containers = int(os.environ.get('MAX_CONTAINERS', 10))
        self.container_lifetime = int(os.environ.get('CONTAINER_LIFETIME', 3600))
        # Clean up any leftover containers on startup
        self._cleanup_leftover_containers()

    def _cleanup_leftover_containers(self):
        """Clean up any containers with our label that might have been left running"""
        try:
            containers = self.client.containers.list(
                all=True,
                filters={
                    'label': ['app=web-terminal']
                }
            )
            logger.info(f"Found {len(containers)} leftover containers to clean up")
            for container in containers:
                try:
                    container.stop(timeout=1)
                    container.remove(force=True)
                    logger.info(f"Cleaned up leftover container: {container.id[:12]}")
                except Exception as e:
                    logger.error(f"Error cleaning up container {container.id[:12]}: {e}")
        except Exception as e:
            logger.error(f"Error listing containers for cleanup: {e}")

    def create_session(self, ws_id, user_id, request=None):
        with self.lock:
            logger.info(f"Creating new session for user {user_id} (ws_id: {ws_id})")

            # Check if we've hit the container limit
            if len(self.sessions) >= self.max_containers:
                logger.warning(f"Maximum container limit ({self.max_containers}) reached")
                raise Exception("Maximum number of containers reached")

            if user_id in self.user_sessions:
                old_ws_id = self.user_sessions[user_id]
                logger.info(f"User {user_id} has existing session {old_ws_id}, cleaning up")
                self.cleanup_session(old_ws_id)

            try:
                image = os.environ.get('CONTAINER_IMAGE', 'ghcr.io/jonasbg/linux-webterminal/terminal-base:latest')
                logger.info(f"Starting container with image: {image}")
                container = self.client.containers.run(
                    image,
                    detach=True,
                    tty=True,
                    stdin_open=True,
                    remove=True,
                    user='1000:1000',
                    labels={
                        'app': 'web-terminal',
                        'ws_id': ws_id,
                        'user_id': user_id,
                        'io.containers.autoupdate': 'image'
                    },
                    security_opt=[
                        'no-new-privileges:true',
                        "mask=/proc/cpuinfo",
                        "mask=/proc/meminfo",
                        "mask=/proc/diskstats",
                        "mask=/proc/modules",
                        "mask=/proc/kallsyms",
                        "mask=/proc/keys",
                        "mask=/proc/drivers",
                        "mask=/proc/net",
                        "mask=/proc/asound",
                        "mask=/proc/key-users",
                        "mask=/proc/slabinfo",
                        "mask=/proc/uptime",
                        "mask=/proc/stat",
                        "mask=/proc/zoneinfo",
                        "mask=/proc/vmallocinfo",
                        "mask=/proc/mounts",
                        "mask=/proc/kpageflags",
                        "mask=/proc/kpagecount",
                        "mask=/proc/kpagecgroup",
                        "mask=/proc/scsi",
                        "mask=/proc/buddyinfo",
                        "mask=/proc/pagetypeinfo",
                        "mask=/proc/ioports",
                        "mask=/proc/iomem",
                        "mask=/proc/interrupts",
                        "mask=/proc/softirqs",
                        "mask=/proc/dma",
                        "mask=/proc/uptime"
                    ],
                    cap_drop=['ALL'],
                    network="none",
                    mem_limit='64m',
                    cpu_period=100000,  # Default CPU CFS period (microseconds)
                    cpu_quota=10000,    # Only allow 10% CPU usage
                    cpu_shares=128,     # Lower CPU priority relative to other containers
                    ulimits=[
                        {'name': 'cpu', 'soft': 10, 'hard': 10},  # Restrict CPU time to 10 seconds
                        {'name': 'nproc', 'soft': 20, 'hard': 20}  # Limit number of processes even more
                    ],
                    pids_limit=10,
                    read_only=True,
                    tmpfs={
                        '/tmp': 'size=64m,noexec,nosuid',
                        '/home': 'size=64m,exec'
                    },
                    environment={
                        "TERM": "xterm",
                        "PS1": "\\w \\$ ",
                        "HOME": "/home/termuser",
                        "PATH": "/usr/local/bin",
                    }
                )

                # First set the terminal size
                stty_exec = self.client.api.exec_create(
                    container.id,
                    'stty cols 142',
                    stdin=True,
                    tty=True
                )
                self.client.api.exec_start(stty_exec['Id'])

                # Then start the shell
                exec_create = self.client.api.exec_create(
                    container.id,
                    '/usr/local/bin/sh -l',
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

                def cleanup_after_lifetime():
                    eventlet.sleep(self.container_lifetime)
                    self.cleanup_session(ws_id)

                eventlet.spawn_n(cleanup_after_lifetime)

                logger.info(f"Container {container.id[:12]} created successfully for user {user_id}")
                return container.id

            except Exception as e:
                logger.error(f"Failed to create container for user {user_id}: {e}")
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
                user_id = session.get('user_id')
                container_id = session.get('container').id[:12] if session.get('container') else 'unknown'

                logger.info(f"Cleaning up session {ws_id} for user {user_id} (container: {container_id})")

                try:
                    if session.get('socket'):
                        try:
                            session['socket']._sock.close()
                            logger.debug(f"Closed socket for session {ws_id}")
                        except Exception as e:
                            logger.error(f"Error closing socket for session {ws_id}: {e}")

                    if session.get('container'):
                        try:
                            session['container'].stop(timeout=1)
                            session['container'].remove(force=True)
                            logger.info(f"Removed container {container_id}")
                        except Exception as e:
                            logger.error(f"Error removing container {container_id}: {e}")

                    if user_id and user_id in self.user_sessions:
                        del self.user_sessions[user_id]
                        logger.info(f"Removed user session mapping for {user_id}")

                except Exception as e:
                    logger.error(f"Error in cleanup for session {ws_id}: {e}")
                finally:
                    del self.sessions[ws_id]
                    logger.info(f"Session {ws_id} cleanup completed")

    def resize_terminal(self, ws_id, cols, rows):
        if ws_id not in self.sessions:
            raise Exception("Session not found")

        session = self.sessions[ws_id]
        try:
            container = session['container']

            # Use both cols and rows in stty command
            stty_exec = self.client.api.exec_create(
                container.id,
                f'stty cols {cols}',
                stdin=True,
                tty=True
            )
            self.client.api.exec_start(stty_exec['Id'])

            # Also try to resize the exec instance for compatibility
            try:
                self.client.api.exec_resize(
                    session['exec_id'],
                    height=rows,
                    width=cols
                )
            except Exception as e:
                logger.debug(f"Exec resize failed (this is normal for some container runtimes): {e}")

        except Exception as e:
            logger.error(f"Error resizing terminal for session {ws_id}: {e}")
            raise

    def _get_docker_client(self):
        # Try different Docker socket locations
        socket_paths = [
            'unix:///var/run/docker.sock',  # Standard Docker socket
            'unix://' + os.path.expanduser('~') + '/.colima/docker.sock',  # Colima socket
            'unix:///run/podman/podman.sock',  # Podman socket
            'unix:///run/user/1000/podman/podman.sock'
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
    logger.info("Initiating shutdown and container cleanup...")
    try:
        session_ids = list(tty_controller.sessions.keys())
        logger.info(f"Cleaning up {len(session_ids)} active sessions")

        for ws_id in session_ids:
            try:
                session = tty_controller.sessions[ws_id]
                container_id = session.get('container').id[:12] if session.get('container') else 'unknown'
                user_id = session.get('user_id', 'unknown')

                logger.info(f"Cleaning up container {container_id} for session {ws_id} (user: {user_id})")

                # Close socket connection
                if session.get('socket'):
                    try:
                        session['socket']._sock.close()
                        logger.debug(f"Closed socket for session {ws_id}")
                    except Exception as e:
                        logger.error(f"Error closing socket: {e}")

                # Stop and remove container
                if session.get('container'):
                    try:
                        session['container'].stop(timeout=1)
                        session['container'].remove(force=True)
                        logger.info(f"Removed container {container_id}")
                    except Exception as e:
                        logger.error(f"Error removing container: {e}")

                # Clean up user session mapping
                if user_id in tty_controller.user_sessions:
                    del tty_controller.user_sessions[user_id]

                # Remove session
                del tty_controller.sessions[ws_id]

            except Exception as e:
                logger.error(f"Error cleaning up session {ws_id}: {e}")

        logger.info("All sessions cleaned up successfully")

    except Exception as e:
        logger.error(f"Error during shutdown cleanup: {e}")
    finally:
        logger.info("Shutdown complete")
        sys.exit(0)

@app.route('/')
def index():
    return render_template('terminal.html')

@socketio.on('resize')
def handle_resize(data):
    ws_id = data.get('id')
    cols = data.get('cols', 142)
    rows = data.get('rows', 24)

    if not ws_id:
        return

    try:
        tty_controller.resize_terminal(ws_id, cols, rows)
    except Exception as e:
        print(f"Error handling resize: {e}")
        emit('error', {'error': str(e)})

@socketio.on('connect')
def handle_connect():
    ws_id = str(uuid.uuid4())
    user_id = request.sid
    client_ip = RequestMetadata.get_client_ip(request)[0]
    logger.info(f"New connection from {client_ip} - ws_id: {ws_id}, user_id: {user_id}")
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
        port = int(os.environ.get('PORT', 5000))
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