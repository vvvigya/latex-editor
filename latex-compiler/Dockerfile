FROM texlive/texlive:latest

# Install Python for the worker script
RUN apt-get update && apt-get install -y \
    python3 \
    python3-pip \
    && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /app

# Copy worker script
COPY worker.py /app/worker.py
RUN chmod +x /app/worker.py

# Create work directory
RUN mkdir -p /work/latex_files

# Set environment variables
ENV LATEX_WORK_DIR=/work/latex_files
ENV PYTHONUNBUFFERED=1

# Run the worker
CMD ["python3", "/app/worker.py"]
