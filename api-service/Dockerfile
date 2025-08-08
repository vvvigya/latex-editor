FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download || true
COPY . .
RUN go build -o /out/api-service .

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /out/api-service /usr/local/bin/api-service
RUN adduser -D appuser && mkdir -p /app && chown -R appuser:appuser /app
USER appuser
EXPOSE 8080
ENV LATEX_FILES_DIR=/app/latex_files
CMD ["/usr/local/bin/api-service"]
