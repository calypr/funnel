FROM python:3.11-slim-bookworm@sha256:f06d01d4ba3f8ad5af45a3ec87df19c1d317d8e8961edc0fabda61811af6f7cf
COPY tmp/requirements.txt /tmp/requirements.txt
COPY utils /utils
RUN pip install --no-cache-dir -r /tmp/requirements.txt pylibjpeg==2.0.1 pylibjpeg-libjpeg==2.1.0
WORKDIR /utils
