FROM ubuntu:22.04

ARG TARGETOS
ARG TARGETARCH

RUN mkdir -p /patch/source

COPY ./22-04/${TARGETARCH}/ubuntu.list    /patch/source/ubuntu.list
COPY ./22-04/${TARGETARCH}/crio.list      /patch/source/crio.list
COPY ./22-04/${TARGETARCH}/keyrings       /patch/keyrings
COPY --chmod=775 patch.sh /patch/patch.sh

USER 0

CMD ["/patch/patch.sh"]
