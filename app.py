import eventlet
eventlet.monkey_patch()
# Eventlet monkey patch must come first
import eventlet
eventlet.monkey_patch()

import sys
import signal
import datetime
import time
import yaml
from pathlib import Path
from logging.handlers import RotatingFileHandler
import logging
from threading import Lock
import uuid
import os
import docker
from flask_cors import CORS
from flask_socketio import SocketIO, emit
from flask import Flask, render_template, request, jsonify, send_from_directory


# Course definitions are loaded from YAML so image refs and metadata can be
# updated without editing application code.
COURSES = {}

def canonical_course_slug(slug):
    return slug


def course_config(slug):
    canonical = canonical_course_slug(slug)
    return canonical, COURSES.get(canonical)


def course_source_dir(slug):
    canonical, config = course_config(slug)
    if not config:
        return canonical
    return config.get('source_dir', canonical)


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

DEFAULT_COURSES_PATH = Path(__file__).with_name("courses.yaml")


def merge_course_overrides(base, override):
    merged = dict(base)
    for slug, course in override.items():
        if slug not in merged:
            merged[slug] = course
            continue
        if not isinstance(merged[slug], dict) or not isinstance(course, dict):
            raise ValueError(f"Course {slug} override must map to an object")
        merged[slug] = {**merged[slug], **course}
    return merged


def load_courses():
    paths_value = os.environ.get("COURSES_PATHS")
    if paths_value:
        paths = [Path(p.strip()) for p in paths_value.split(":") if p.strip()]
    else:
        paths = [Path(os.environ.get("COURSES_PATH", DEFAULT_COURSES_PATH))]

    if not paths:
        raise ValueError("No course configuration paths provided")

    data = {}
    for path in paths:
        if not path.exists():
            raise FileNotFoundError(f"Course configuration not found: {path}")
        with path.open(encoding="utf-8") as f:
            loaded = yaml.safe_load(f) or {}
        if not isinstance(loaded, dict):
            raise ValueError(f"Course configuration must be a mapping: {path}")
        data = merge_course_overrides(data, loaded)

    required_fields = {
        "title", "description", "long_description", "image",
        "profile", "group", "order", "guides",
    }

    for slug, course in data.items():
        if not isinstance(course, dict):
            raise ValueError(f"Course {slug} must map to an object")
        missing = required_fields - course.keys()
        if missing:
            raise ValueError(f"Course {slug} missing required fields: {sorted(missing)}")
        if not isinstance(course["guides"], list):
            raise ValueError(f"Course {slug} guides must be a list")

    logger.info("Loaded course configuration from %s", ", ".join(str(p) for p in paths))
    return data


COURSES = load_courses()

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
        'X-Original-Forwarded-For',  # Modified forwarded header
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
    """A logger class for recording terminal sessions and interactions.

    This class provides functionality to log terminal sessions, including commands
    and their outputs, along with session metadata like container ID, user ID,
    and client information. The logging can be enabled/disabled via environment
    variables.

    Environment Variables:
        TTY_LOGGING_ENABLED (str): Set to 'true' to enable logging (default: 'false')
        TTY_LOG_DIR (str): Directory path for storing log files (default: './logs')

    Attributes:
        enabled (bool): Whether logging is enabled
        log_dir (Path): Path object pointing to the log directory

    Methods:
        create_session_log(container_id, user_id, ws_id, request=None):
            Creates a new log file for a terminal session with metadata.
        log_command(filename, command, output):
            Logs a command and its output to the specified log file.
        clean_terminal_output(text, command=None):
            Cleans terminal output by removing control sequences and formatting.
    """

    def __init__(self):
        self.enabled = os.environ.get(
            'TTY_LOGGING_ENABLED', 'false').lower() == 'true'
        self.log_dir = Path(os.environ.get('TTY_LOG_DIR', './logs'))
        if self.enabled:
            self.log_dir.mkdir(exist_ok=True)

    def create_session_log(self, container_id, user_id, ws_id, request=None):
        if not self.enabled:
            return None

        timestamp = datetime.datetime.now().strftime('%Y%m%d-%H%M%S')
        filename = self.log_dir / f"{timestamp}-{container_id[:12]}.log"

        with open(filename, 'w', encoding='utf-8') as f:
            f.write(f"Session Start: {timestamp}\n")
            f.write(f"Container ID: {container_id}\n")
            f.write(f"User ID: {user_id}\n")
            f.write(f"WebSocket ID: {ws_id}\n")

            if request:
                metadata = RequestMetadata.get_request_metadata(request)
                f.write(
                    f"Origin IP: {metadata['ip_address']} (via {metadata['ip_source']})\n")
                f.write(f"User Agent: {metadata['user_agent']}\n")
            else:
                f.write("Origin IP: Not available\n")
                f.write("User Agent: Not available\n")

            f.write("\n=== Command History ===\n\n")

        return filename

    def log_command(self, filename, command, output):
        if not self.enabled or not filename:
            return

        timestamp = datetime.datetime.now().strftime('%H:%M:%S')
        with open(filename, 'a') as f:
            f.write(f"[{timestamp}] > {command.strip()}\n")
            if output:
                cleaned_output = self.clean_terminal_output(output, command)
                if cleaned_output.strip():  # Only write if there's non-empty output
                    f.write(f"{cleaned_output.strip()}\n")

    def clean_terminal_output(self, text, command=None):
        """Remove terminal control sequences, colors, and prompt for clean log output."""
        import re

        # Strip ANSI color codes
        # This pattern matches all ANSI escape sequences for colors
        ansi_escape = re.compile(r'\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])')
        text = ansi_escape.sub('', text)

        # Remove specific control sequences
        text = text.replace('\x1b[6n', '')  # Remove DSR (Device Status Report)
        text = text.replace('\x1b[J', '')   # Remove clear screen
        text = text.replace('\x1b[K', '')   # Remove clear line
        text = text.replace('\x07', '')     # Remove bell
        text = text.replace('\a', '')       # Remove bell (alternative)
        # Convert carriage returns to newlines
        text = text.replace('\r', '\n')

        # Remove terminal prompt lines
        lines = []
        for line in text.split('\n'):
            # Skip prompt lines (matches various forms of the prompt)
            if re.search(r'termuser@container.*\$', line) or re.search(r'~\$', line):
                continue
            # Remove backspace characters and the characters they erase
            while '\b' in line:
                line = re.sub('.\b', '', line)
            lines.append(line)

        # Join lines and remove any command echo if provided
        text = '\n'.join(lines)
        if command and command.strip():
            # Remove the command from the beginning of the output
            text = re.sub(r'^' + re.escape(command.strip()),
                          '', text, flags=re.MULTILINE)

        # Remove empty lines at beginning and end
        text = text.strip()

        # Collapse multiple consecutive empty lines into one
        text = re.sub(r'\n\s*\n\s*\n+', '\n\n', text)

        return text


class TTYController:
    """
    TTYController manages Docker container-based terminal sessions for a web terminal application.

    This class handles the creation, management, and cleanup of Docker containers that provide
    terminal access through WebSocket connections. It supports multiple shells per session and
    implements security measures to contain and limit resource usage.

    Attributes:
        client (docker.DockerClient): Docker client instance for container operations
        sessions (dict): Dictionary storing active terminal sessions
        user_sessions (dict): Maps user IDs to their active session IDs
        lock (threading.Lock): Thread synchronization lock
        logger (TTYLogger): Logger instance for session and command logging
        max_containers (int): Maximum number of concurrent containers allowed
        container_lifetime (int): Container lifetime in seconds before automatic cleanup

    Environment Variables:
        MAX_CONTAINERS: Maximum number of concurrent containers (default: 10)
        CONTAINER_LIFETIME: Container lifetime in seconds (default: 3600)
        CONTAINER_IMAGE: Docker image to use for containers

    Security Features:
        - No network access
        - Read-only filesystem with limited tmpfs
        - CPU and memory limits
        - Process limitations
        - Masked proc filesystem entries
        - No privileges escalation
        - Dropped capabilities

    Methods:
        create_session(ws_id, user_id, request): Creates a new container session
        create_shell(ws_id, tab_id, user_id, request): Creates a new shell in existing session
        write_to_container(ws_id, data): Writes data to container
        read_from_container(ws_id): Reads data from container
        write_to_shell(ws_id, shell_id, data): Writes data to specific shell
        read_from_shell(ws_id, shell_id): Reads data from specific shell
        cleanup_session(ws_id): Cleans up a session and its container
        resize_terminal(ws_id, cols, rows): Resizes terminal dimensions
        resize_shell(ws_id, shell_id, cols, rows): Resizes specific shell dimensions
        close_shell(ws_id, shell_id): Closes a specific shell in a session

    Example:
        controller = TTYController()
        session_id = controller.create_session("ws1", "user1")
        controller.write_to_container("ws1", "ls\n")
        output = controller.read_from_container("ws1")
        controller.cleanup_session("ws1")
    """

    def __init__(self):
        self.client = self._get_docker_client()
        self.sessions = {}
        self.user_sessions = {}
        self.lock = Lock()
        self.logger = TTYLogger()
        # Get limits from environment
        self.max_containers = int(os.environ.get('MAX_CONTAINERS', 10))
        self.container_lifetime = int(
            os.environ.get('CONTAINER_LIFETIME', 3600))
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
            logger.info(
                "Found %d leftover containers to clean up", len(containers))
            for container in containers:
                try:
                    container.stop(timeout=1)
                    container.remove(force=True)
                    logger.info(
                        "Cleaned up leftover container: %s", container.id[:12])
                except Exception as e:
                    logger.error(
                        "Error cleaning up container %s: %s", container.id[:12], e)
        except Exception as e:
            logger.error("Error listing containers for cleanup: %s", e)

    def _get_container_kwargs(self, profile, ws_id, user_id, image, overrides=None):
        """Build container run kwargs based on security profile."""
        base_kwargs = {
            'detach': True,
            'tty': True,
            'stdin_open': True,
            'remove': True,
            'labels': {
                'app': 'web-terminal',
                'ws_id': ws_id,
                'user_id': user_id,
                'io.containers.autoupdate': 'image',
            },
            'environment': {
                "TERM": "xterm",
                "PS1": "\\w \\$ ",
                "HOME": "/home/termuser",
                "PATH": "/usr/local/bin",
            },
        }

        if profile == 'builder':
            # Narrower builder profile for podman-based workshop images.
            # Privileged mode is still required for nested container builds here,
            # but we keep resource limits and avoid host user namespace sharing.
            base_kwargs.update({
                'user': '0:0',
                'network': 'bridge',
                'privileged': True,
                'read_only': False,
                'cpu_period': 100000,
                'cpu_quota': 50000,
                'cpu_shares': 512,
                'pids_limit': 128,
                'mem_limit': '768m',
                'tmpfs': {
                    "/run": "rw,nosuid,nodev,exec,mode=755",
                    "/var/lib/containers": "rw,nosuid,nodev,exec,mode=755",
                    "/tmp": "size=256m,rw,nosuid,nodev",
                    "/home": "size=128m,rw,exec,nosuid,nodev",
                },
            })
        else:
            # Strict config for Linux I/II and default
            base_kwargs.update({
                'user': '1000:1000',
                'network': 'none',
                'cap_drop': ['ALL'],
                'security_opt': [
                    'no-new-privileges:true',
                    "mask=/proc/cpuinfo", "mask=/proc/meminfo",
                    "mask=/proc/diskstats", "mask=/proc/modules",
                    "mask=/proc/kallsyms", "mask=/proc/keys",
                    "mask=/proc/drivers", "mask=/proc/net",
                    "mask=/proc/asound", "mask=/proc/key-users",
                    "mask=/proc/slabinfo", "mask=/proc/uptime",
                    "mask=/proc/stat", "mask=/proc/zoneinfo",
                    "mask=/proc/vmallocinfo", "mask=/proc/mounts",
                    "mask=/proc/kpageflags", "mask=/proc/kpagecount",
                    "mask=/proc/kpagecgroup", "mask=/proc/scsi",
                    "mask=/proc/buddyinfo", "mask=/proc/pagetypeinfo",
                    "mask=/proc/ioports", "mask=/proc/iomem",
                    "mask=/proc/interrupts", "mask=/proc/softirqs",
                    "mask=/proc/dma",
                ],
                'cpu_period': 100000,
                'cpu_quota': 10000,
                'cpu_shares': 128,
                'pids_limit': 10,
                'mem_limit': '64m',
                'read_only': True,
                'tmpfs': {
                    '/tmp': 'size=64m,noexec,nosuid',
                    '/home': 'size=64m,exec',
                },
            })

        # Apply per-course overrides
        if overrides:
            base_kwargs.update(overrides)

        return base_kwargs

    def create_session(self, ws_id, user_id, request=None, course=None):
        # Check limits and clean up old session under lock
        with self.lock:
            if len(self.sessions) >= self.max_containers:
                raise Exception("Maximum number of containers reached")

            if user_id in self.user_sessions:
                old_ws_id = self.user_sessions[user_id]
                logger.info(
                    "User %s has existing session %s, cleaning up", user_id, old_ws_id)
                self.cleanup_session(old_ws_id)

        try:
            # Resolve image and security profile from course config
            _, config = course_config(course)
            resolved_course = config or {}
            profile = resolved_course.get('profile', 'strict')
            image = resolved_course.get('image') or os.environ.get(
                'CONTAINER_IMAGE', 'git.torden.tech/jonasbg/terminal-linux-1:latest')

            overrides = {}
            if resolved_course.get('pids_limit'):
                overrides['pids_limit'] = resolved_course['pids_limit']
            if resolved_course.get('mem_limit'):
                overrides['mem_limit'] = resolved_course['mem_limit']

            logger.info("Starting container with image: %s (profile: %s)", image, profile)

            kwargs = self._get_container_kwargs(profile, ws_id, user_id, image, overrides)
            container = self.client.containers.run(image, **kwargs)

            # Start the shell (client sends resize after container_ready)
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

            log_file = self.logger.create_session_log(
                container.id,
                user_id,
                ws_id,
                request
            )

            # Only hold lock for dict mutations
            with self.lock:
                self.sessions[ws_id] = {
                    'container': container,
                    'exec_id': exec_create['Id'],
                    'socket': exec_socket,
                    'user_id': user_id,
                    'log_file': log_file,
                    'current_command': '',
                    'buffer': '',
                    'command_complete': False,
                    'main_shell_alive': True
                }
                self.user_sessions[user_id] = ws_id

            def cleanup_after_lifetime():
                eventlet.sleep(self.container_lifetime)
                self.cleanup_session(ws_id)

            eventlet.spawn_n(cleanup_after_lifetime)

            logger.info(
                "Container %s created for user %s", container.id[:12], user_id)
            return container.id

        except Exception as e:
            logger.error(
                "Failed to create container for user %s: %s", user_id, e)
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
                # Store the complete command for reference, but don't mark as complete yet
                session['pending_command'] = session['current_command'].strip()
                session['current_command'] = ''
                # Reset output buffer for new command
                session['output_buffer'] = ''
                # Start a timer to associate output with this command
                session['command_start_time'] = time.time()
            elif '\x7f' in data or '\x08' in data:  # Backspace (DEL or BS)
                if session['current_command']:
                    session['current_command'] = session['current_command'][:-1]
            # Arrow keys or other escape sequences
            elif data.startswith('\x1b['):
                # Don't add escape sequences to command
                pass
            else:
                session['current_command'] += data

            socket._sock.send(data.encode())
        except Exception as e:
            logger.error("Error writing to container: %s", e)
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
                # Check if we need to finalize previous command
                self._check_command_timeout(ws_id, session)
                return None

            data = socket._sock.recv(4096)
            if not data:
                raise EOFError("Shell exited")

            output = data.decode('utf-8', errors='replace')

            # Add output to the buffer for the current command
            if 'output_buffer' not in session:
                session['output_buffer'] = ''
            session['output_buffer'] += output

            # If we are tracking a pending command, check if this output contains a new prompt
            # which indicates the command is finished executing
            if session.get('pending_command') is not None and self._contains_prompt(output):
                self._finalize_command(ws_id, session)

            # Check for timeout even if we received data
            self._check_command_timeout(ws_id, session)

            return output
        except (EOFError, BrokenPipeError, ConnectionResetError, OSError):
            raise  # Let connection errors propagate to break the read loop
        except Exception as e:
            logger.error("Error reading from container: %s", e)
            return None

    def _contains_prompt(self, text):
        """Check if the output contains a terminal prompt, indicating command completion"""
        # Match the terminal prompt patterns
        import re
        return bool(re.search(r'termuser@container.*\$', text)) or bool(re.search(r'~\$', text))

    def _check_command_timeout(self, ws_id, session):
        """Check if a command has timed out and should be finalized"""
        # If we've been waiting for output for more than 1 second, consider the command done
        if (session.get('pending_command') is not None and
                session.get('command_start_time') is not None and
                time.time() - session['command_start_time'] > 1.0):
            self._finalize_command(ws_id, session)

    def _finalize_command(self, _, session):
        """Finalize a command by logging it with its output"""
        if session.get('pending_command') is not None:
            # Log the command and its output
            self.logger.log_command(
                session['log_file'],
                session['pending_command'],
                session['output_buffer']
            )
            # Reset for next command
            session['pending_command'] = None
            session['output_buffer'] = ''
            session['command_start_time'] = None

    def cleanup_session(self, ws_id):
        with self.lock:
            if ws_id in self.sessions:
                session = self.sessions[ws_id]
                user_id = session.get('user_id')
                container_id = session.get('container').id[:12] if session.get(
                    'container') else 'unknown'

                logger.info(
                    "Cleaning up session %s for user %s (container: %s)", ws_id, user_id, container_id)

                try:
                    if session.get('socket'):
                        try:
                            session['socket']._sock.close()
                            logger.debug("Closed socket for session %s", ws_id)
                        except Exception as e:
                            logger.error(
                                "Error closing socket for session %s: %s", ws_id, e)

                    if session.get('container'):
                        try:
                            session['container'].stop(timeout=1)
                            session['container'].remove(force=True)
                            logger.info("Removed container %s", container_id)
                        except Exception as e:
                            logger.error(
                                "Error removing container %s: %s", container_id, e)

                    if user_id and user_id in self.user_sessions:
                        del self.user_sessions[user_id]
                        logger.info(
                            "Removed user session mapping for %s", user_id)

                except Exception as e:
                    logger.error(
                        "Error in cleanup for session %s: %s", ws_id, e)
                finally:
                    del self.sessions[ws_id]
                    logger.info("Session %s cleanup completed", ws_id)

    def resize_terminal(self, ws_id, cols, rows):
        if ws_id not in self.sessions:
            raise Exception("Session not found")

        session = self.sessions[ws_id]
        try:
            container = session['container']

            # Use both cols and rows in stty command
            stty_exec = self.client.api.exec_create(
                container.id,
                f'stty cols {cols} rows {rows}',
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
                logger.debug(
                    "Exec resize failed (this is normal for some container runtimes): %s", e)

        except Exception as e:
            logger.error(
                "Error resizing terminal for session %s: %s", ws_id, e)
            raise

    def _get_docker_client(self):
        # Try different Docker socket locations
        socket_paths = [
            'unix:///var/run/docker.sock',  # Standard Docker socket
            # Colima socket
            'unix://' + os.path.expanduser('~') + '/.colima/docker.sock',
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

    def create_shell(self, ws_id, tab_id, user_id, request=None):
        """Create a new shell in an existing container session"""
        # Read session state under lock, then release before Docker API calls
        with self.lock:
            if ws_id not in self.sessions:
                raise Exception(f"Session not found: {ws_id}")
            session = self.sessions[ws_id]
            container = session.get('container')
            if not container:
                raise Exception("Container not found in session")

        try:
            # Create a new shell process (client sends resize after)
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

            shell_id = f"shell-{uuid.uuid4()}"

            log_file = self.logger.create_session_log(
                container.id,
                user_id,
                shell_id,
                request
            )

            # Only hold lock for the dict mutation
            with self.lock:
                if 'shells' not in session:
                    session['shells'] = {}
                session['shells'][shell_id] = {
                    'exec_id': exec_create['Id'],
                    'socket': exec_socket,
                    'tab_id': tab_id,
                    'log_file': log_file,
                    'current_command': '',
                    'buffer': '',
                    'command_complete': False
                }

            logger.info(
                "Shell %s created for session %s", shell_id, ws_id)
            return shell_id

        except Exception as e:
            logger.error(
                "Failed to create shell for session %s: %s", ws_id, e)
            raise

    def _check_shell_command_timeout(self, ws_id, shell_id, shell):
        """Check if a command in a shell has timed out and should be finalized"""
        # If we've been waiting for output for more than 1 second, consider the command done
        if (shell.get('pending_command') is not None and
                shell.get('command_start_time') is not None and
                time.time() - shell.get('command_start_time') > 1.0):
            self._finalize_shell_command(ws_id, shell_id, shell)

    def _finalize_shell_command(self, ws_id, shell_id, shell):
        """Finalize a command in a shell by logging it with its output"""
        if shell.get('pending_command') is not None:
            # Log the command and its output
            self.logger.log_command(
                shell.get('log_file'),
                shell.get('pending_command'),
                shell.get('output_buffer')
            )
            # Reset for next command
            shell['pending_command'] = None
            shell['output_buffer'] = ''
            shell['command_start_time'] = None

    def resize_shell(self, ws_id, shell_id, cols, rows):
        """Resize a specific shell in a session"""
        if ws_id not in self.sessions:
            raise Exception("Session not found")

        session = self.sessions[ws_id]

        if 'shells' not in session or shell_id not in session['shells']:
            raise Exception("Shell not found")

        shell = session['shells'][shell_id]
        container = session['container']

        try:
            # Use both cols and rows in stty command
            stty_exec = self.client.api.exec_create(
                container.id,
                f'stty cols {cols} rows {rows}',
                stdin=True,
                tty=True
            )
            self.client.api.exec_start(stty_exec['Id'])

            # Also try to resize the exec instance for compatibility
            try:
                self.client.api.exec_resize(
                    shell['exec_id'],
                    height=rows,
                    width=cols
                )
            except Exception as e:
                logger.debug(
                    "Exec resize failed (this is normal for some container runtimes): %s", e)

        except Exception as e:
            logger.error("Error resizing shell %s: %s", shell_id, e)
            raise

    def is_session_dead(self, ws_id):
        """Check if all shells (main + additional) in a session have exited"""
        if ws_id not in self.sessions:
            return True
        session = self.sessions[ws_id]
        if session.get('main_shell_alive'):
            return False
        shells = session.get('shells', {})
        return len(shells) == 0

    def close_shell(self, ws_id, shell_id):
        """Close a specific shell in a session"""
        with self.lock:
            if ws_id not in self.sessions:
                logger.warning(
                    "Session %s not found when closing shell %s", ws_id, shell_id)
                return

            session = self.sessions[ws_id]

            if 'shells' not in session or shell_id not in session['shells']:
                logger.warning(
                    "Shell %s not found in session %s", shell_id, ws_id)
                return

            shell = session['shells'][shell_id]
            container = session.get('container')

            try:
                logger.info("Closing shell %s in session %s", shell_id, ws_id)

                # First try to terminate the shell gracefully
                if shell.get('socket'):
                    try:
                        # Send Ctrl+D (EOF) followed by exit command
                        shell['socket']._sock.send(b'\x04')
                        time.sleep(0.1)
                        shell['socket']._sock.send(b'exit\r\n')
                        time.sleep(0.1)

                        # Close the socket
                        shell['socket']._sock.close()
                        logger.debug("Closed socket for shell %s", shell_id)
                    except Exception as e:
                        logger.debug(
                            "Error closing socket for shell %s: %s", shell_id, e)

                # If we have exec_id, try to forcefully terminate if possible
                if container and shell.get('exec_id'):
                    try:
                        # Try to execute a ps command to find and kill the shell process
                        kill_exec = self.client.api.exec_create(
                            container.id,
                            "pkill -P $(ps -o pid= -p $(ps -o ppid= -p $$))",
                            stdin=False,
                            tty=True
                        )
                        self.client.api.exec_start(kill_exec['Id'])
                    except Exception as e:
                        logger.error(
                            "Error terminating process for shell %s: %s", shell_id, e)

                # Remove shell from session
                del session['shells'][shell_id]
                logger.info("Shell %s closed successfully", shell_id)

            except Exception as e:
                logger.error("Error closing shell %s: %s", shell_id, e)

    # Modified cleanup_session to handle shells
    def cleanup_session_with_shells(self, ws_id):
        with self.lock:
            if ws_id in self.sessions:
                session = self.sessions[ws_id]
                user_id = session.get('user_id')
                container_id = session.get('container').id[:12] if session.get(
                    'container') else 'unknown'

                logger.info(
                    "Cleaning up session %s for user %s (container: %s)", ws_id, user_id, container_id)

                try:
                    # Close all shell sockets
                    if 'shells' in session:
                        for shell_id, shell in list(session['shells'].items()):
                            try:
                                if shell.get('socket'):
                                    shell['socket']._sock.close()
                                    logger.debug(
                                        "Closed socket for shell %s", shell_id)
                            except Exception as e:
                                logger.error(
                                    "Error closing socket for shell %s: %s", shell_id, e)

                    if session.get('container'):
                        try:
                            session['container'].stop(timeout=1)
                            session['container'].remove(force=True)
                            logger.info("Removed container %s", container_id)
                        except Exception as e:
                            logger.error(
                                "Error removing container %s: %s", container_id, e)

                    if user_id and user_id in self.user_sessions:
                        del self.user_sessions[user_id]
                        logger.info(
                            "Removed user session mapping for %s", user_id)

                except Exception as e:
                    logger.error(
                        "Error in cleanup for session %s: %s", ws_id, e)
                finally:
                    del self.sessions[ws_id]
                    logger.info("Session %s cleanup completed", ws_id)

    def write_to_shell(self, ws_id, shell_id, data):
        """Write data to a specific shell in a session"""
        if ws_id not in self.sessions:
            raise Exception("Session not found")

        session = self.sessions[ws_id]

        if 'shells' not in session or shell_id not in session['shells']:
            raise Exception("Shell not found")

        shell = session['shells'][shell_id]

        try:
            socket = shell['socket']
            if not socket:
                raise Exception("Socket not connected")

            # Handle command building for logging
            if '\r' in data or '\n' in data:  # Enter key pressed
                # Store the complete command for reference, but don't mark as complete yet
                shell['pending_command'] = shell['current_command'].strip()
                shell['current_command'] = ''
                # Reset output buffer for new command
                shell['output_buffer'] = ''
                # Start a timer to associate output with this command
                shell['command_start_time'] = time.time()
            elif '\x7f' in data or '\x08' in data:  # Backspace (DEL or BS)
                if shell['current_command']:
                    shell['current_command'] = shell['current_command'][:-1]
            # Arrow keys or other escape sequences
            elif data.startswith('\x1b['):
                # Don't add escape sequences to command
                pass
            else:
                shell['current_command'] += data

            socket._sock.send(data.encode())
        except Exception as e:
            logger.error("Error writing to shell %s: %s", shell_id, e)
            raise

    def read_from_shell(self, ws_id, shell_id):
        """Read data from a specific shell in a session"""
        if ws_id not in self.sessions:
            return None

        session = self.sessions[ws_id]

        if 'shells' not in session or shell_id not in session['shells']:
            return None

        shell = session['shells'][shell_id]

        try:
            socket = shell['socket']
            if not socket:
                return None

            import select
            readable, _, _ = select.select([socket._sock], [], [], 0.1)
            if not readable:
                # Check if we need to finalize previous command
                self._check_shell_command_timeout(ws_id, shell_id, shell)
                return None

            data = socket._sock.recv(4096)
            if not data:
                raise EOFError("Shell exited")

            output = data.decode('utf-8', errors='replace')

            # Add output to the buffer for the current command
            if 'output_buffer' not in shell:
                shell['output_buffer'] = ''
            shell['output_buffer'] += output

            # If we are tracking a pending command, check if this output contains a new prompt
            # which indicates the command is finished executing
            if shell.get('pending_command') is not None and self._contains_prompt(output):
                self._finalize_shell_command(ws_id, shell_id, shell)

            # Check for timeout even if we received data
            self._check_shell_command_timeout(ws_id, shell_id, shell)

            return output
        except (EOFError, BrokenPipeError, ConnectionResetError, OSError):
            raise  # Let connection errors propagate to break the read loop
        except Exception as e:
            logger.error("Error reading from shell %s: %s", shell_id, e)
            return None


tty_controller = TTYController()


def cleanup_all_containers(signum, frame):
    logger.info("Initiating shutdown and container cleanup...")
    try:
        session_ids = list(tty_controller.sessions.keys())
        logger.info("Cleaning up %d active sessions", len(session_ids))

        for ws_id in session_ids:
            try:
                session = tty_controller.sessions[ws_id]
                container_id = session.get('container').id[:12] if session.get(
                    'container') else 'unknown'
                user_id = session.get('user_id', 'unknown')

                logger.info(
                    "Cleaning up container %s for session %s (user: %s)", container_id, ws_id, user_id)

                # Clean up all shells
                if 'shells' in session:
                    for shell_id, shell in list(session['shells'].items()):
                        try:
                            if shell.get('socket'):
                                shell['socket']._sock.close()
                                logger.debug(
                                    "Closed socket for shell %s", shell_id)
                        except Exception as e:
                            logger.error("Error closing shell socket: %s", e)

                # Clean up main socket if it exists (for backward compatibility)
                if session.get('socket'):
                    try:
                        session['socket']._sock.close()
                        logger.debug("Closed socket for session %s", ws_id)
                    except Exception as e:
                        logger.error("Error closing socket: %s", e)

                # Stop and remove container
                if session.get('container'):
                    try:
                        session['container'].stop(timeout=1)
                        session['container'].remove(force=True)
                        logger.info("Removed container %s", container_id)
                    except Exception as e:
                        logger.error("Error removing container: %s", e)

                # Clean up user session mapping
                if user_id in tty_controller.user_sessions:
                    del tty_controller.user_sessions[user_id]

                # Remove session
                del tty_controller.sessions[ws_id]

            except Exception as e:
                logger.error("Error cleaning up session %s: %s", ws_id, e)

        logger.info("All sessions cleaned up successfully")

    except Exception as e:
        logger.error("Error during shutdown cleanup: %s", e)
    finally:
        logger.info("Shutdown complete")
        sys.exit(0)


@app.route('/')
def index():
    return render_template('index.html')


@app.route('/terminal')
def terminal():
    return render_template('terminal.html')


@app.route('/api/courses')
def api_courses():
    courses_list = []
    for slug, config in COURSES.items():
            courses_list.append({
            'slug': slug,
            'title': config['title'],
            'description': config['description'],
            'long_description': config.get('long_description', config['description']),
            'profile': config['profile'],
            'group': config.get('group', 'Other'),
            'order': config.get('order', 999),
            'has_guide': bool(config.get('guides')),
        })
    courses_list.sort(key=lambda c: (c['order'], c['title']))
    return jsonify(courses_list)


@app.route('/api/courses/<slug>/readme')
def api_course_readme(slug):
    canonical, config = course_config(slug)
    if not config:
        return jsonify({'error': 'Not found'}), 404
    readme_path = os.path.join('courses', course_source_dir(canonical), 'README.md')
    if not os.path.isfile(readme_path):
        return jsonify({'error': 'No README'}), 404
    with open(readme_path, 'r') as f:
        content = f.read()
    # Parse YAML frontmatter
    meta = {}
    body = content
    if content.startswith('---'):
        parts = content.split('---', 2)
        if len(parts) >= 3:
            body = parts[2].strip()
            for line in parts[1].strip().split('\n'):
                if ':' in line:
                    key, val = line.split(':', 1)
                    val = val.strip()
                    # Parse arrays like [a, b, c]
                    if val.startswith('[') and val.endswith(']'):
                        val = [v.strip() for v in val[1:-1].split(',')]
                    meta[key.strip()] = val
    return jsonify({'meta': meta, 'body': body})


# Cache for guide files extracted from container images
_guide_cache = {}


@app.route('/api/courses/<slug>/files')
def api_course_files(slug):
    canonical, config = course_config(slug)
    if not config:
        return jsonify([]), 404
    guide_paths = config.get('guides', [])
    if not guide_paths:
        return jsonify([])

    # Return cached if available
    if canonical in _guide_cache:
        return jsonify(_guide_cache[canonical])

    # Extract files from the container image
    image = config['image']
    files = []
    for path in guide_paths:
        try:
            result = tty_controller.client.containers.run(
                image, f'/usr/local/bin/busybox cat {path}',
                remove=True, stdout=True, stderr=False
            )
            content = result.decode('utf-8', errors='replace')
            name = os.path.basename(path)
            files.append({'name': name, 'content': content})
        except Exception as e:
            logger.error("Error extracting %s from %s: %s", path, image, e)

    _guide_cache[canonical] = files
    return jsonify(files)


@app.route('/api/courses/<slug>/images/<path:filename>')
def api_course_image(slug, filename):
    canonical, config = course_config(slug)
    if not config:
        return 'Not found', 404
    return send_from_directory(os.path.join('courses', course_source_dir(canonical), 'images'), filename)


@socketio.on('connect')
def handle_connect():
    ws_id = str(uuid.uuid4())
    user_id = request.sid
    client_ip = RequestMetadata.get_client_ip(request)[0]
    logger.info(
        "New connection from %s - ws_id: %s, user_id: %s", client_ip, ws_id, user_id)
    emit('session_id', {'id': ws_id, 'user_id': user_id})


@socketio.on('start_session')
def handle_start_session(data):
    ws_id = data['id']
    user_id = request.sid
    course = canonical_course_slug(data.get('course'))  # Optional course slug from client
    try:
        container_id = tty_controller.create_session(ws_id, user_id, request, course=course)
        logger.info(
            "Created container %s for session %s (user: %s)", container_id, ws_id, user_id)

        # Start the original read loop
        def read_output(ws_id, user_id):
            with app.app_context():
                while True:
                    try:
                        output = tty_controller.read_from_container(ws_id)
                        if output:
                            socketio.emit(
                                'output', {'output': output}, room=user_id)
                    except Exception as e:
                        logger.info("Main shell exited for session %s: %s", ws_id, e)
                        break
                    socketio.sleep(0.05)
                # Mark main shell as dead
                if ws_id in tty_controller.sessions:
                    tty_controller.sessions[ws_id]['main_shell_alive'] = False
                if tty_controller.is_session_dead(ws_id):
                    socketio.emit('session_ended', {}, room=user_id)
                else:
                    socketio.emit('main_shell_exited', {}, room=user_id)

        socketio.start_background_task(read_output, ws_id, user_id)

        # Send the container ready event
        emit('container_ready')
    except Exception as e:
        logger.error("Error creating session: %s", e)
        emit('error', {'error': str(e)})


@socketio.on('create_shell')
def handle_create_shell(data):
    containerId = data.get('containerId')  # This is the ws_id
    tab_id = data.get('tabId')
    user_id = request.sid

    if not containerId or not tab_id:
        emit('error', {'error': 'Missing containerId (ws_id) or tab ID'})
        return

    try:
        shell_id = tty_controller.create_shell(
            containerId, tab_id, user_id, request)
        emit('shell_created', {'tabId': tab_id, 'shellId': shell_id})

        def read_shell_output(ws_id, shell_id, user_id):
            with app.app_context():
                while True:
                    try:
                        output = tty_controller.read_from_shell(
                            ws_id, shell_id)
                        if output:
                            socketio.emit('shell_output', {
                                'shellId': shell_id,
                                'output': output
                            }, room=user_id)
                    except Exception as e:
                        logger.info("Shell %s exited: %s", shell_id, e)
                        break
                    socketio.sleep(0.05)
                # Remove shell from session
                if ws_id in tty_controller.sessions:
                    session = tty_controller.sessions[ws_id]
                    shells = session.get('shells', {})
                    shells.pop(shell_id, None)
                socketio.emit('shell_exited', {
                    'shellId': shell_id
                }, room=user_id)
                if tty_controller.is_session_dead(ws_id):
                    socketio.emit('session_ended', {}, room=user_id)

        socketio.start_background_task(
            read_shell_output, containerId, shell_id, user_id)

    except Exception as e:
        logger.error("Error creating shell: %s", e)
        emit('error', {'error': str(e)})


@socketio.on('shell_input')
def handle_shell_input(data):
    containerId = data.get('containerId')  # This is the ws_id
    shell_id = data.get('shellId')
    user_input = data.get('input')

    if not containerId or not shell_id or not user_input:
        return

    try:
        tty_controller.write_to_shell(containerId, shell_id, user_input)
    except Exception as e:
        logger.error("Error handling shell input: %s", e)
        emit('error', {'error': str(e)})


@socketio.on('resize_shell')
def handle_resize_shell(data):
    containerId = data.get('containerId')  # This is the ws_id
    shell_id = data.get('shellId')
    cols = data.get('cols', 142)
    rows = data.get('rows', 24)

    if not containerId or not shell_id:
        return

    if containerId not in tty_controller.sessions:
        return

    try:
        tty_controller.resize_shell(containerId, shell_id, cols, rows)
    except Exception as e:
        logger.error("Error handling shell resize: %s", e)
        emit('error', {'error': str(e)})


@socketio.on('close_shell')
def handle_close_shell(data):
    containerId = data.get('containerId')  # This is the ws_id
    shell_id = data.get('shellId')

    if not containerId or not shell_id:
        return

    try:
        # Removed the user_id parameter
        tty_controller.close_shell(containerId, shell_id)
    except Exception as e:
        logger.error("Error closing shell: %s", e)
        emit('error', {'error': str(e)})


@socketio.on('input')
def handle_input(data):
    ws_id = data.get('id')
    user_input = data.get('input')

    if not ws_id or not user_input:
        return

    if ws_id not in tty_controller.sessions:
        return

    try:
        tty_controller.write_to_container(ws_id, user_input)
    except Exception as e:
        logger.error("Error handling input: %s", e)
        emit('error', {'error': str(e)})


@socketio.on('resize')
def handle_resize(data):
    ws_id = data.get('id')
    cols = data.get('cols', 142)
    rows = data.get('rows', 24)

    if not ws_id:
        return

    if ws_id not in tty_controller.sessions:
        return

    try:
        tty_controller.resize_terminal(ws_id, cols, rows)
    except Exception as e:
        logger.error("Error handling resize: %s", e)
        emit('error', {'error': str(e)})


@socketio.on('disconnect')
def handle_disconnect():
    user_id = request.sid
    logger.info("Client disconnected (user: %s)", user_id)
    if user_id in tty_controller.user_sessions:
        ws_id = tty_controller.user_sessions[user_id]
        # Use the original cleanup method
        tty_controller.cleanup_session(ws_id)


signal.signal(signal.SIGINT, cleanup_all_containers)
signal.signal(signal.SIGTERM, cleanup_all_containers)

if __name__ == '__main__':
    try:
        port = int(os.environ.get('PORT', 8080))
        app.logger.info("Server starting on port %d", port)

        socketio.run(
            app,
            host='0.0.0.0',
            port=port,
            debug=False,
            use_reloader=False,
            log_output=True
        )
    except Exception as e:
        app.logger.error("Server failed to start: %s", e)
    finally:
        cleanup_all_containers(None, None)
