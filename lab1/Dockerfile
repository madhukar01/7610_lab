FROM ubuntu:latest

RUN apt-get update && apt-get install -y gcc

COPY ./src /app

WORKDIR /app

RUN gcc -o main main.c lib/log.c -Wall -Wextra

ENTRYPOINT ["./main"]
