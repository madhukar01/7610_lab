// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

interface IOracle {
    // Events
    event RequestCreated(bytes32 indexed requestId, address indexed requester, string prompt);
    event ResponseReceived(bytes32 indexed requestId, string response, string ipfsCid);
    event OracleNodeRegistered(address indexed node);
    event OracleNodeRemoved(address indexed node);

    // Structs
    struct Request {
        address requester;
        string prompt;
        uint256 timestamp;
        bool fulfilled;
        string response;
        string ipfsCid;
    }

    // Core functionality
    function createRequest(string calldata prompt) external payable returns (bytes32 requestId);
    function submitResponse(bytes32 requestId, string calldata response, string calldata ipfsCid) external;
    function getRequest(bytes32 requestId) external view returns (Request memory);

    // View functions
    function getFee() external view returns (uint256);
}
