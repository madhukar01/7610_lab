FROM golang:1.23.2-alpine

COPY ./src /app

WORKDIR /app

RUN go mod download

RUN go build -o /app/main

EXPOSE 8080 8081

ENTRYPOINT ["/app/main"]
