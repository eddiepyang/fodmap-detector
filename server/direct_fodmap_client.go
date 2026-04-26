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

// LookupFODMAP implements the FodmapSessionClient interface by calling the
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
		Ingredient:    ingredient,
		Found:         true,
		FodmapLevel:   res.Level,
		FodmapGroups:  res.Groups,
		Notes:         res.Notes,
		Substitutions: res.Substitutions,
	}, nil
}
