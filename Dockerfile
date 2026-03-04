FROM golang:1.24 AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .
RUN CGO_ENABLED=0 go build -o fx-quotes ./cmd/server

FROM gcr.io/distroless/base-debian12

WORKDIR /app
COPY --from=builder /app/fx-quotes /app/fx-quotes

EXPOSE 8080

ENTRYPOINT ["/app/fx-quotes"]
