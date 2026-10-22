# syntax=docker/dockerfile:1
# Build context: the ml/ directory.
FROM python:3.12-slim
WORKDIR /app
COPY serve/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY serve/server.py ./serve/
COPY model/labels.json model/leafwise-mnv3s.onnx ./model/
ENV MODEL_DIR=/app/model PORT=8000
EXPOSE 8000
USER 65534
HEALTHCHECK CMD python -c "import urllib.request;urllib.request.urlopen('http://127.0.0.1:8000/healthz')"
CMD ["python", "serve/server.py"]
