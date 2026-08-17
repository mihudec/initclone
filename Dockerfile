ARG RCLONE_VERSION=1.74.2
FROM rclone/rclone:${RCLONE_VERSION} AS rclone

FROM python:3.13.7-alpine3.22

RUN apk add --no-cache ca-certificates
COPY --from=rclone /usr/local/bin/rclone /usr/local/bin/rclone
COPY requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir --disable-pip-version-check -r /tmp/requirements.txt \
    && rm /tmp/requirements.txt
COPY initclone.py /usr/local/bin/initclone

# Root is intentional: applying the requested UID/GID to mounted volumes requires it.
USER 0:0
ENTRYPOINT ["python3", "/usr/local/bin/initclone"]
