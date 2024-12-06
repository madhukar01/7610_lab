// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

import "@openzeppelin/contracts/access/Ownable.sol";
import "./IOracle.sol";

/**
 * @title Oracle
 * @dev Smart contract for decentralized LLM oracle with PBFT consensus
 */
contract Oracle is IOracle, Ownable {
    // State variables
    mapping(bytes32 => Request) private requests;
    address private oracleAddress;
    uint256 private requestFee;

    constructor(
        uint256 _requestFee,
        address _oracleAddress
    ) Ownable(msg.sender) {
        require(_oracleAddress != address(0), "Invalid oracle address");
        oracleAddress = _oracleAddress;
        requestFee = _requestFee;
    }

    // Core functionality
    function createRequest(string calldata prompt)
        external
        payable
        override
        returns (bytes32 requestId)
    {
        require(msg.value >= requestFee, "Insufficient fee");
        require(bytes(prompt).length > 0, "Empty prompt");

        requestId = keccak256(abi.encodePacked(prompt, msg.sender, block.timestamp));
        require(requests[requestId].requester == address(0), "Request already exists");

        requests[requestId] = Request({
            requester: msg.sender,
            prompt: prompt,
            timestamp: block.timestamp,
            fulfilled: false,
            response: "",
            ipfsCid: ""
        });

        emit RequestCreated(requestId, msg.sender, prompt);
        return requestId;
    }

    function submitResponse(
        bytes32 requestId,
        string calldata response,
        string calldata ipfsCid
    ) external override {
        require(msg.sender == oracleAddress, "Not authorized");
        require(!requests[requestId].fulfilled, "Request already fulfilled");
        require(bytes(response).length > 0, "Empty response");
        require(bytes(ipfsCid).length > 0, "Empty IPFS CID");

        requests[requestId].fulfilled = true;
        requests[requestId].response = response;
        requests[requestId].ipfsCid = ipfsCid;

        emit ResponseReceived(requestId, response, ipfsCid);

        // Transfer fee to oracle
        payable(oracleAddress).transfer(requestFee);
    }

    function getRequest(bytes32 requestId)
        external
        view
        override
        returns (Request memory)
    {
        return requests[requestId];
    }

    // Oracle management
    function updateOracleAddress(address newAddress) external onlyOwner {
        require(newAddress != address(0), "Invalid oracle address");
        emit OracleNodeRemoved(oracleAddress);
        oracleAddress = newAddress;
        emit OracleNodeRegistered(newAddress);
    }

    // View functions
    function getOracleAddress() external view returns (address) {
        return oracleAddress;
    }

    function getFee() external view override returns (uint256) {
        return requestFee;
    }

    receive() external payable {}
}
