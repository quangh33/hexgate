FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
#builds the binary to a file named /hexgate
RUN CGO_ENABLED=0 go build -o /app/hexgate .

FROM gcr.io/distroless/static-debian11
WORKDIR /app
COPY --from=builder /app/config ./config
COPY --from=builder /app/hexgate .

CMD ["./hexgate"]