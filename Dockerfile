FROM golang:1.9.2-alpine as builder
COPY glide.* /go/src/github.com/erickfaustino/urd/
COPY main.go /go/src/github.com/erickfaustino/urd/
WORKDIR /go/src/github.com/erickfaustino/urd
RUN apk add --no-cache \
  --virtual build-deps \
  gcc \
  git \
  ca-certificates \
  wget \
  && wget -qO- https://github.com/Masterminds/glide/releases/download/v0.12.3/glide-v0.12.3-linux-amd64.tar.gz \
  | tar xvz --strip-components=1 -C /go/bin/ linux-amd64/glide \
  && glide install
  RUN CGO_ENABLED=0 GOOS=linux go build -v -a --installsuffix cgo --ldflags="-s" -o urd

  FROM alpine:latest
  RUN apk add --no-cache ca-certificates
  COPY --from=builder /go/src/github.com/erickfaustino/urd/urd /usr/bin/urd
  ENTRYPOINT ["/usr/bin/urd"]
