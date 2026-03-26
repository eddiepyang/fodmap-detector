package server

import (
	"context"
	"fodmap/chat"
)

// DirectFodmapClient is an adapter that implements chat.FodmapServerClient
// by calling the server's Searcher directly, avoiding an HTTP round-trip.
type DirectFodmapClient struct {
	s *Server
}

// NewDirectFodmapClient creates a new DirectFodmapClient.
func NewDirectFodmapClient(s *Server) *DirectFodmapClient {
	return &DirectFodmapClient{s: s}
}

// FetchTopBusiness is not implemented, as it's not needed for the direct client.
func (c *DirectFodmapClient) FetchTopBusiness(ctx context.Context, query, category, city, state string) (*chat.Business, error) {
	// This method is not used by the chat session's tool calls.
	// The business search happens before the chat session is created.
	return nil, nil
}

// FetchChatReviews is not implemented, as it's not needed for the direct client.
func (c *DirectFodmapClient) FetchChatReviews(ctx context.Context, businessID, query string, limit int) ([]chat.Review, error) {
	// This method is not used by the chat session's tool calls.
	// The review search happens before the chat session is created.
	return nil, nil
}

// LookupFODMAP implements the FodmapServerClient interface by calling the
// server's searcher directly.
func (c *DirectFodmapClient) LookupFODMAP(ctx context.Context, ingredient string) (chat.FodmapToolResponse, error) {
	res, _, err := c.s.searcher.SearchFodmap(ctx, ingredient)
	if err != nil {
		return chat.FodmapToolResponse{}, err
	}

	if res.Ingredient == "" {
		return chat.FodmapToolResponse{
			Ingredient: ingredient,
			Found:      false,
			Message:    "ingredient not in database; consult the Monash University FODMAP app for accurate classification",
		}, nil
	}

	return chat.FodmapToolResponse{
		Ingredient:   ingredient,
		Found:        true,
		FodmapLevel:  res.Level,
		FodmapGroups: res.Groups,
		Notes:        res.Notes,
	}, nil
}
