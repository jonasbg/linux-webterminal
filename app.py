# Eventlet monkey patch must come first
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

class TTYController:
    def __init__(self):
        self.client = self._get_docker_client()
        self.sessions = {}
        self.user_sessions = {}  # New dict to track user sessions
        self.lock = Lock()

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

    def create_session(self, ws_id, user_id):  # Add user_id parameter
        with self.lock:
            if user_id in self.user_sessions:
                # Clean up old session if user already has one
                old_ws_id = self.user_sessions[user_id]
                self.cleanup_session(old_ws_id)

            try:
                print(f"Creating container for session {ws_id} (user: {user_id})")
                # Create container with security constraints
                container = self.client.containers.run(
                    'terminal-base:latest',
                    detach=True,
                    tty=True,
                    stdin_open=True,
                    remove=True,
                    # Security configurations
                    user='1000:1000',  # Non-root user
                    security_opt=['no-new-privileges:true'],
                    cap_drop=['ALL'],  # Drop all capabilities
                    mem_limit='64m',  # Memory limit
                    pids_limit=100,  # Process limit
                    read_only=True,  # Read-only root filesystem
                    tmpfs={
                        '/tmp': 'size=64m,noexec,nosuid',
                        '/home': 'size=64m,exec'
                    },  # Temporary writable storage
                    environment={
                        "TERM": "xterm",
                        "PS1": "\\w \\$ ",
                        "HOME": "/home/termuser",
                    }
                )
                print(f"Container created with ID: {container.id}")

                # Get low-level API client
                api_client = self.client.api

                # Create exec instance
                print("Creating exec instance")
                exec_create = api_client.exec_create(
                    container.id,
                    '/bin/bash -l',
                    stdin=True,
                    tty=True
                )
                print(f"Exec instance created with ID: {exec_create['Id']}")

                # Start exec instance with socket
                print("Starting exec instance")
                exec_socket = api_client.exec_start(
                    exec_create['Id'],
                    socket=True,
                    tty=True
                )
                print("Exec instance started successfully")

                self.sessions[ws_id] = {
                    'container': container,
                    'exec_id': exec_create['Id'],
                    'socket': exec_socket,
                    'user_id': user_id  # Add user_id to session info
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
            # Use select to check if there's data available to read
            readable, _, _ = select.select([socket._sock], [], [], 0.1)
            if not readable:
                return None

            data = socket._sock.recv(4096)
            if not data:
                return None

            return data.decode('utf-8', errors='replace')
        except Exception as e:
            print(f"Error reading from container: {e}")
            return None

    def cleanup_session(self, ws_id):
        with self.lock:
            if ws_id in self.sessions:
                session = self.sessions[ws_id]
                try:
                    # Get user_id before cleanup
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

                    # Remove user from user_sessions if exists
                    if user_id and user_id in self.user_sessions:
                        del self.user_sessions[user_id]

                    print(f"Cleaned up session {ws_id} for user {user_id}")
                except Exception as e:
                    print(f"Error in cleanup: {e}")
                finally:
                    del self.sessions[ws_id]

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
    user_id = request.sid  # Use Socket.IO session ID as user identifier
    print(f"New connection: {ws_id} (user: {user_id})")
    emit('session_id', {'id': ws_id, 'user_id': user_id})

@socketio.on('start_session')
def handle_start_session(data):
    ws_id = data['id']
    user_id = request.sid
    try:
        container_id = tty_controller.create_session(ws_id, user_id)
        print(f"Created container {container_id} for session {ws_id} (user: {user_id})")

        def read_output(ws_id, user_id):  # Add user_id parameter
            with app.app_context():
                while True:
                    try:
                        output = tty_controller.read_from_container(ws_id)
                        if output:
                            # Emit only to the specific user's socket
                            socketio.emit('output', {'output': output}, room=user_id)
                    except Exception as e:
                        print(f"Error in read loop: {e}")
                        break
                    socketio.sleep(0.05)

        # Pass both ws_id and user_id to read_output
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
