FROM pytorch/pytorch:2.1.2-cuda12.1-cudnn8-runtime

WORKDIR /workspace

COPY stress_gpu.py /workspace/stress_gpu.py

ENV PYTHONUNBUFFERED=1

CMD ["python", "/workspace/stress_gpu.py"]
