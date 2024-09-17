## CS 7610: Lab 1
- Name: Madhukara S Holla
- Email: sholla.m@northeastern.edu

## Directory Structure
```
lab1/
├── src/
│   ├── lib/
│   │   ├── log.c
│   │   ├── log.h
│   ├── main.c
│   ├── peer_program.h
│   ├── hostsfile.txt
├── docker-compose.yml
├── Dockerfile
├── Readme.md
├── Readme.pdf
├── report.pdf

```
- `src/` contains the source code for the peer program.
    - `src/lib/` contains external libraries. ([log.c and log.h for logging](https://github.com/rxi/log.c))
    - `src/main.c` contains the main function for the peer program.
    - `src/peer_program.h` is the header file.
    - `src/hostsfile.txt` is the hosts file.
- `docker-compose.yml` docker compose file for running the containers.
- `Dockerfile` is the Dockerfile to build the docker image.

## Instructions to run the code
1. Open the terminal in the `lab1/` directory.
2. Build the docker image using the command `docker build -t prj1 .`
2. Run `docker compose up` to bring up the containers and view the output in the terminal.

## Notes
1. Only `READY` is printed to `stderr`, everything else logged.
2. Line 7 in `main.c` sets the logging level to `LOG_ERROR` - any fatal errors in the program are shown in the docker logs.
3. Program runs indefinitely until stopped by the user.
4. Peer names in `hostsfile.txt` should match container names in the docker compose file (not case-sensitive).
