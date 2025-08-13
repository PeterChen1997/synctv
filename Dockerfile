FROM node:18-alpine AS web-builder

WORKDIR /synctv-web

# 首先复制 package files 以便更好地利用 Docker 缓存
COPY synctv-web/package*.json ./

RUN npm ci || npm install

# 然后复制其他源文件
COPY synctv-web/ ./

RUN npm run build

FROM golang:1.24-alpine AS builder

ARG VERSION

WORKDIR /synctv

COPY ./ ./

RUN apk add --no-cache bash curl git g++

COPY --from=web-builder /synctv-web/dist/ /synctv/public/dist/

RUN curl -sL \
    https://raw.githubusercontent.com/zijiren233/go-build-action/refs/tags/v1/build.sh | \
    bash -s -- \
    --version=${VERSION} \
    --use-default-cc-cxx \
    --bin-name-no-suffix

FROM alpine:latest

ENV PUID=0 PGID=0 UMASK=022

COPY --from=builder /synctv/build/synctv /usr/local/bin/synctv

RUN apk add --no-cache bash ca-certificates su-exec tzdata && \
    rm -rf /var/cache/apk/*

COPY script/entrypoint.sh /entrypoint.sh

RUN chmod +x /entrypoint.sh && \
    mkdir -p /root/.synctv

WORKDIR /root/.synctv

EXPOSE 8080/tcp

VOLUME [ "/root/.synctv" ]

ENTRYPOINT [ "/entrypoint.sh" ]

CMD [ "server" ]
