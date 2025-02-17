from flask import Flask, render_template
from flask_socketio import SocketIO, emit
from flask_cors import CORS
import docker
import os
import uuid
from threading import Lock

app = Flask(__name__)
CORS(app, resources={r"/*": {"origins": "*"}})
app.config['SECRET_KEY'] = os.urandom(24)
socketio = SocketIO(app, cors_allowed_origins="*")

class TTYController:
    def __init__(self):
        self.client = self._get_docker_client()
        self.sessions = {}
        self.lock = Lock()

    def _get_docker_client(self):
        # Try different Docker socket locations
        socket_paths = [
            'unix:///var/run/docker.sock',  # Standard Docker socket
            'unix:///Users/jonasbg/.colima/docker.sock',  # Colima socket
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

    def create_session(self, ws_id):
        with self.lock:
            try:
                print(f"Creating container for session {ws_id}")
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
                        '/home/termuser': 'size=64m,exec,uid=1000,gid=1000'  # Add this line
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
                    'socket': exec_socket
                }

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

                    print(f"Cleaned up session {ws_id}")
                except Exception as e:
                    print(f"Error in cleanup: {e}")
                finally:
                    del self.sessions[ws_id]

tty_controller = TTYController()

@app.route('/')
def index():
    return render_template('terminal.html')

@socketio.on('connect')
def handle_connect():
    ws_id = str(uuid.uuid4())
    print(f"New connection: {ws_id}")
    emit('session_id', {'id': ws_id})

@socketio.on('start_session')
def handle_start_session(data):
    ws_id = data['id']
    try:
        container_id = tty_controller.create_session(ws_id)
        print(f"Created container {container_id} for session {ws_id}")

        def read_output(ws_id):
            with app.app_context():
                while True:
                    try:
                        output = tty_controller.read_from_container(ws_id)
                        if output:
                            socketio.emit('output', {'output': output})
                    except Exception as e:
                        print(f"Error in read loop: {e}")
                        break
                    socketio.sleep(0.05)  # Sleep for 50ms between reads

        socketio.start_background_task(read_output, ws_id)
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
    print("Client disconnected")
    for ws_id in list(tty_controller.sessions.keys()):
        tty_controller.cleanup_session(ws_id)

if __name__ == '__main__':
    print("\nServer starting on port 5000")
    print("Open http://localhost:5000 in your browser\n")
    socketio.run(app, debug=True)