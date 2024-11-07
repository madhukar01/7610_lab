## CS 7610: Lab 4
- Name: Madhukara S Holla
- Email: sholla.m@northeastern.edu

## Directory Structure
```
lab2/
├── src/
│   ├── main.go
│   ├── hostsfile-testcase1.txt
│   ├── hostsfile-testcase2.txt
│   ├── go.mod
├── Dockerfile
├── Readme.md
├── Readme.pdf
├── report.pdf
├── docker-compose-testcase-*.yml

```
- `src/` contains the source code.
    - `src/main.go` contains the main function with membership protocol.
    - `src/hostsfile-testcase1.txt` is the hosts file for testcase 1.
    - `src/hostsfile-testcase2.txt` is the hosts file for testcase 2.
- `docker-compose-testcase-*.yml` docker compose file for running the containers for the testcases.
- `Dockerfile` is the Dockerfile to build the docker image.

## Instructions to run the code
1. Open the terminal in the `lab4/` directory.
2. Build the docker image using the command `docker build -t prj4 .`
3. Run `docker compose -f <compose file> up` to bring up the containers and view the output in the terminal.
