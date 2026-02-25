FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates tcpdump bash && \
    rm -rf /var/lib/apt/lists/*

RUN mkdir -p /captures

COPY capture-controller /usr/local/bin/capture-controller

USER root

ENTRYPOINT ["/usr/local/bin/capture-controller"]

