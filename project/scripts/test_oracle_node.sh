#!/bin/bash

cd ../oracle

# Run unit tests
echo "Running unit tests..."
make test-unit

# Run integration tests
echo "Running integration tests..."
make test-integration

# Build and run a local test cluster
echo "Building and running test cluster..."
make build
./bin/oracle-node --config config/examples/node1.json &
./bin/oracle-node --config config/examples/node2.json &
./bin/oracle-node --config config/examples/node3.json &
./bin/oracle-node --config config/examples/node4.json &

# Wait for cluster to stabilize
sleep 5

# TODO: Add test requests here

# Cleanup
pkill oracle-node
