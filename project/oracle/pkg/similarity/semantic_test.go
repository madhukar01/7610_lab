package similarity

import (
	"context"
	"testing"
)

func TestSemanticScorer(t *testing.T) {
	apiKey := "test-key"
	scorer := NewSemanticScorer(apiKey, "")

	// Test responses
	responses := []*Response{
		{
			Text:   "The capital of France is Paris.",
			NodeID: "node1",
		},
		{
			Text:   "Paris is the capital city of France.",
			NodeID: "node2",
		},
		{
			Text:   "The capital of France is Paris, also known as the City of Light.",
			NodeID: "node3",
		},
		{
			Text:   "London is the capital of England.",
			NodeID: "node4",
		},
		{
			Text:   "The UK's capital city is London.",
			NodeID: "node5",
		},
	}

	ctx := context.Background()

	// Test clustering
	clusters, err := scorer.ClusterResponses(ctx, responses, 0.85)
	if err != nil {
		t.Fatalf("Failed to cluster responses: %v", err)
	}

	// Should have 2 clusters (Paris and London)
	if len(clusters) != 2 {
		t.Errorf("Expected 2 clusters, got %d", len(clusters))
	}

	// First cluster should have 3 responses (Paris)
	if len(clusters[0].Responses) != 3 {
		t.Errorf("Expected largest cluster to have 3 responses, got %d", len(clusters[0].Responses))
	}

	// Second cluster should have 2 responses (London)
	if len(clusters[1].Responses) != 2 {
		t.Errorf("Expected second cluster to have 2 responses, got %d", len(clusters[1].Responses))
	}

	// Test consensus selection
	consensus := scorer.SelectConsensusResponse(clusters)
	if consensus == nil {
		t.Fatal("Expected consensus response, got nil")
	}

	// Consensus should be from the largest cluster (Paris)
	found := false
	for _, resp := range clusters[0].Responses {
		if resp == consensus {
			found = true
			break
		}
	}
	if !found {
		t.Error("Consensus response not found in largest cluster")
	}
}

func TestCosineSimilarity(t *testing.T) {
	// Test identical vectors
	v1 := []float32{1, 2, 3}
	sim := cosineSimilarity(v1, v1)
	if sim != 1 {
		t.Errorf("Expected similarity 1 for identical vectors, got %f", sim)
	}

	// Test orthogonal vectors
	v2 := []float32{1, 0, 0}
	v3 := []float32{0, 1, 0}
	sim = cosineSimilarity(v2, v3)
	if sim != 0 {
		t.Errorf("Expected similarity 0 for orthogonal vectors, got %f", sim)
	}

	// Test zero vector
	v4 := []float32{0, 0, 0}
	sim = cosineSimilarity(v1, v4)
	if sim != 0 {
		t.Errorf("Expected similarity 0 with zero vector, got %f", sim)
	}

	// Test different lengths
	v5 := []float32{1, 2}
	sim = cosineSimilarity(v1, v5)
	if sim != 0 {
		t.Errorf("Expected similarity 0 for vectors of different lengths, got %f", sim)
	}
}
