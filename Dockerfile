FROM python:3.9-slim

# Create user with same UID as the host user running podman
RUN groupadd -g 1001 appuser && \
    useradd -u 1000 -g appuser -s /bin/bash -m appuser && \
    # Add appuser to root group to access docker socket
    usermod -a -G root appuser

WORKDIR /app

# Copy requirements first to leverage Docker cache
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy application code
COPY . .

# Set ownership to appuser
RUN chown -R appuser:appuser /app

# Switch to non-root user
USER appuser

# Expose the port the app runs on
EXPOSE 5000

# Command to run the application
CMD ["gunicorn", "--workers", "8", "-k", "eventlet", "--bind", "0.0.0.0:5000", "app:app"]
