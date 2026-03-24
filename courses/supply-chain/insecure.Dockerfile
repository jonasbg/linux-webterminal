FROM python:3.12

WORKDIR /app

RUN pip install flask requests

COPY <<'EOF' /app/app.py
from flask import Flask

app = Flask(__name__)

@app.route("/")
def hello():
    return "hello from insecure image\n"

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=8080)
EOF

EXPOSE 8080

CMD ["python", "/app/app.py"]
