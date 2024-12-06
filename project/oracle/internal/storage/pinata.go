package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

const (
	pinataBaseURL = "https://api.pinata.cloud"
)

// PinataClient handles IPFS storage operations using Pinata
type PinataClient struct {
	apiKey    string
	apiSecret string
	jwt       string
	client    *http.Client
}

// NewPinataClient creates a new storage client using Pinata
func NewPinataClient(apiKey, apiSecret, jwt string) (StorageClient, error) {
	if jwt == "" && (apiKey == "" || apiSecret == "") {
		return nil, fmt.Errorf("either JWT or both API key and secret must be provided")
	}

	return &PinataClient{
		apiKey:    apiKey,
		apiSecret: apiSecret,
		jwt:       jwt,
		client:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// StoreExecutionRecord stores the complete execution record in IPFS via Pinata and returns the CID
func (p *PinataClient) StoreExecutionRecord(ctx context.Context, record *ExecutionRecord) (string, error) {
	// Convert data to JSON
	jsonData, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("failed to marshal execution record: %w", err)
	}

	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add the file
	part, err := writer.CreateFormFile("file", "execution_record.json")
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := part.Write(jsonData); err != nil {
		return "", fmt.Errorf("failed to write JSON data: %w", err)
	}

	// Add metadata
	metadata := map[string]interface{}{
		"name": fmt.Sprintf("execution_record_%s", record.RequestID),
		"keyvalues": map[string]string{
			"requestId":  record.RequestID,
			"timestamp":  record.Timestamp.Format(time.RFC3339),
			"recordType": "oracle_execution",
		},
	}
	metadataField, err := writer.CreateFormField("pinataMetadata")
	if err != nil {
		return "", fmt.Errorf("failed to create metadata field: %w", err)
	}
	if err := json.NewEncoder(metadataField).Encode(metadata); err != nil {
		return "", fmt.Errorf("failed to encode metadata: %w", err)
	}

	// Close multipart writer
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", pinataBaseURL+"/pinning/pinFileToIPFS", body)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	if p.jwt != "" {
		req.Header.Set("Authorization", "Bearer "+p.jwt)
	} else {
		req.Header.Set("pinata_api_key", p.apiKey)
		req.Header.Set("pinata_secret_api_key", p.apiSecret)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Send request
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("pinata API error: status=%d body=%s", resp.StatusCode, string(body))
	}

	// Parse response
	var pinataResp struct {
		IpfsHash  string `json:"IpfsHash"`
		PinSize   int    `json:"PinSize"`
		Timestamp string `json:"Timestamp"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pinataResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return pinataResp.IpfsHash, nil
}

// GetExecutionRecord retrieves an execution record from IPFS via Pinata gateway
func (p *PinataClient) GetExecutionRecord(ctx context.Context, cid string) (*ExecutionRecord, error) {
	// Create request to Pinata gateway
	url := fmt.Sprintf("https://gateway.pinata.cloud/ipfs/%s", cid)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Send request
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("pinata gateway error: status=%d body=%s", resp.StatusCode, string(body))
	}

	// Parse the JSON data
	var record ExecutionRecord
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		return nil, fmt.Errorf("failed to unmarshal execution record: %w", err)
	}

	return &record, nil
}
