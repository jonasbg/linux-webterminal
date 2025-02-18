FROM python:3.9-slim

WORKDIR /app

# Copy requirements first to leverage Docker cache
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy application code
COPY . .

# Expose the port the app runs on
EXPOSE 5000

# Fix the Gunicorn command format
CMD ["gunicorn", "--workers", "4", "--bind", "0.0.0.0:5000", "app:app", "--reload"]