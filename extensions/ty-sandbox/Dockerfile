FROM golang:1.24-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /ty-sandbox ./cmd/main.go

FROM alpine:3.21

RUN apk add --no-cache git nodejs npm

# Install Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code

COPY --from=builder /ty-sandbox /usr/local/bin/ty-sandbox

WORKDIR /workspace
EXPOSE 8080

ENTRYPOINT ["ty-sandbox"]
CMD ["--addr", ":8080"]
