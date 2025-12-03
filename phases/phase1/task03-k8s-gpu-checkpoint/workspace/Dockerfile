FROM pytorch/pytorch:2.1.2-cuda12.1-cudnn8-runtime

WORKDIR /workspace

ENV PYTHONUNBUFFERED=1

CMD ["/bin/bash", "-c", "python /workspace/scripts/stress_gpu.py"]
