FROM golang:alpine as build-env
MAINTAINER Jan Soendermann <jan.soendermann+git@gmail.com>
RUN mkdir -p /usr/src/app
WORKDIR /usr/src/app
COPY . .
RUN go build -o hapttic .

FROM alpine
RUN mkdir -p /usr/src/app && apk add --no-cache \
  bash \
  jq \
  curl \
  && rm -rf /var/cache/apk/*
WORKDIR /usr/src/app
COPY --from=build-env /usr/src/app/hapttic .
EXPOSE 8080
ENTRYPOINT ["/usr/src/app/hapttic"]
