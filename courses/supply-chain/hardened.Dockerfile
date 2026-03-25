FROM python:3.12-alpine

WORKDIR /app

RUN addgroup -S app && adduser -S -G app app && \
    pip install --no-cache-dir flask==3.0.3 requests==2.32.5

COPY <<'EOF' /app/app.py
from flask import Flask

app = Flask(__name__)

@app.route("/")
def hello():
    return "hello from hardened image\n"

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=8080)
EOF

USER app

EXPOSE 8080

CMD ["python", "/app/app.py"]
