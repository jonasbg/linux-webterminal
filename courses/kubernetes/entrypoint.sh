#!/usr/local/bin/sh
# Start the mock API server in the background
/usr/local/bin/kube-mock &
# Keep the container alive (web terminal execs a shell separately)
exec /usr/local/bin/sleep infinity
