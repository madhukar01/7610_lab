package similarity

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/sashabaranov/go-openai"
)

// SemanticScorer handles semantic similarity scoring
type SemanticScorer struct {
	client *openai.Client
	model  openai.EmbeddingModel
}

// NewSemanticScorer creates a new semantic scorer
func NewSemanticScorer(apiKey string, model string) *SemanticScorer {
	if model == "" {
		model = string(openai.AdaEmbeddingV2)
	}
	return &SemanticScorer{
		client: openai.NewClient(apiKey),
		model:  openai.EmbeddingModel(model),
	}
}

// Response represents a single response with its node ID
type Response struct {
	Text   string
	NodeID string
}

// Cluster represents a group of similar responses
type Cluster struct {
	Responses      []*Response
	AverageScore   float32
	Representative *Response
}

// GetEmbedding gets the embedding vector for a text
func (s *SemanticScorer) GetEmbedding(ctx context.Context, text string) ([]float32, error) {
	resp, err := s.client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
		Model: s.model,
		Input: text,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get embedding: %w", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embedding data returned")
	}

	return resp.Data[0].Embedding, nil
}

// CosineSimilarity calculates the cosine similarity between two vectors
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct float32
	var normA float32
	var normB float32

	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}

// ClusterResponses clusters responses based on semantic similarity
func (s *SemanticScorer) ClusterResponses(ctx context.Context, responses []*Response, threshold float32) ([]*Cluster, error) {
	if len(responses) == 0 {
		return nil, fmt.Errorf("no responses to cluster")
	}

	// Get embeddings for all responses
	embeddings := make(map[string][]float32)
	for _, resp := range responses {
		embedding, err := s.GetEmbedding(ctx, resp.Text)
		if err != nil {
			return nil, fmt.Errorf("failed to get embedding for response from %s: %w", resp.NodeID, err)
		}
		embeddings[resp.NodeID] = embedding
	}

	// Calculate similarities between all pairs
	similarities := make(map[string]map[string]float32)
	for i, resp1 := range responses {
		similarities[resp1.NodeID] = make(map[string]float32)
		for j, resp2 := range responses {
			if i == j {
				similarities[resp1.NodeID][resp2.NodeID] = 1.0
				continue
			}
			sim := CosineSimilarity(embeddings[resp1.NodeID], embeddings[resp2.NodeID])
			similarities[resp1.NodeID][resp2.NodeID] = sim
		}
	}

	// Cluster responses
	var clusters []*Cluster
	used := make(map[string]bool)

	for _, resp := range responses {
		if used[resp.NodeID] {
			continue
		}

		// Start new cluster
		cluster := &Cluster{
			Responses:      []*Response{resp},
			Representative: resp,
		}
		used[resp.NodeID] = true

		// Find similar responses
		for _, other := range responses {
			if used[other.NodeID] {
				continue
			}

			if similarities[resp.NodeID][other.NodeID] >= threshold {
				cluster.Responses = append(cluster.Responses, other)
				used[other.NodeID] = true
			}
		}

		// Calculate average similarity within cluster
		var totalSim float32
		count := 0
		for i, r1 := range cluster.Responses {
			for j, r2 := range cluster.Responses {
				if i < j {
					totalSim += similarities[r1.NodeID][r2.NodeID]
					count++
				}
			}
		}
		if count > 0 {
			cluster.AverageScore = totalSim / float32(count)
		} else {
			cluster.AverageScore = 1.0 // Single response cluster
		}

		clusters = append(clusters, cluster)
	}

	// Sort clusters by size and average similarity
	sort.Slice(clusters, func(i, j int) bool {
		if len(clusters[i].Responses) != len(clusters[j].Responses) {
			return len(clusters[i].Responses) > len(clusters[j].Responses)
		}
		return clusters[i].AverageScore > clusters[j].AverageScore
	})

	return clusters, nil
}

// SelectConsensusResponse selects the best response from the clusters
func (s *SemanticScorer) SelectConsensusResponse(clusters []*Cluster) *Response {
	if len(clusters) == 0 || len(clusters[0].Responses) == 0 {
		return nil
	}

	// Return the representative response from the largest cluster
	return clusters[0].Representative
}
