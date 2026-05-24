// Package fake provides an in-memory implementation of lightrag.Client for use in tests.
package fake

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

// Client is an in-memory implementation of lightrag.Client.
type Client struct {
	mu               sync.Mutex
	docs             map[string]lightrag.Document
	entities         map[string]lightrag.Entity
	edges            map[string]lightrag.Edge
	matches          []lightrag.Match
	describeResponse string
	describeSources  []string
	nextID           int
}

// New returns a ready-to-use fake Client.
func New() *Client {
	return &Client{
		docs:     map[string]lightrag.Document{},
		entities: map[string]lightrag.Entity{},
		edges:    map[string]lightrag.Edge{},
	}
}

func (c *Client) nextStr(prefix string) string {
	c.nextID++
	return prefix + "-" + strconv.Itoa(c.nextID)
}

// SeedEntity pre-populates the entity store.
func (c *Client) SeedEntity(e lightrag.Entity) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entities[e.ID] = e
}

// SeedMatches sets the matches returned by Query.
func (c *Client) SeedMatches(m []lightrag.Match) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.matches = m
}

// SeedDescribe pre-loads the canned describe response and sources for QueryDescribe.
func (c *Client) SeedDescribe(response string, sources []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.describeResponse = response
	c.describeSources = append([]string(nil), sources...)
}

// InsertDocument stores documents and returns their assigned IDs.
func (c *Client) InsertDocument(_ context.Context, req lightrag.InsertRequest) (*lightrag.InsertResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]string, 0, len(req.Documents))
	for _, d := range req.Documents {
		if d.ID == "" {
			d.ID = c.nextStr("doc")
		}
		c.docs[d.ID] = d
		ids = append(ids, d.ID)
	}
	return &lightrag.InsertResponse{IDs: ids}, nil
}

// GetDocument retrieves a document by ID.
func (c *Client) GetDocument(_ context.Context, id string) (*lightrag.Document, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.docs[id]
	if !ok {
		return nil, &lightrag.HTTPError{Status: 404, Path: "/documents/" + id, Body: "not found"}
	}
	return &d, nil
}

// DeleteDocument removes a document by ID.
func (c *Client) DeleteDocument(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.docs[id]; !ok {
		return &lightrag.HTTPError{Status: 404, Path: "/documents/" + id, Body: "not found"}
	}
	delete(c.docs, id)
	return nil
}

// Query returns the matches seeded via SeedMatches.
func (c *Client) Query(_ context.Context, _ lightrag.QueryRequest) (*lightrag.QueryResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &lightrag.QueryResponse{Matches: append([]lightrag.Match(nil), c.matches...)}, nil
}

// QueryDescribe returns the response seeded via SeedDescribe.
func (c *Client) QueryDescribe(_ context.Context, _ lightrag.QueryRequest) (*lightrag.DescribeResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &lightrag.DescribeResponse{Response: c.describeResponse, Sources: append([]string(nil), c.describeSources...)}, nil
}

// ListEntities returns all entities matching the optional query string.
func (c *Client) ListEntities(_ context.Context, q string) ([]lightrag.Entity, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]lightrag.Entity, 0, len(c.entities))
	for _, e := range c.entities {
		if q == "" || containsFold(e.Name, q) {
			out = append(out, e)
		}
	}
	return out, nil
}

// GetEntity retrieves an entity by ID.
func (c *Client) GetEntity(_ context.Context, id string) (*lightrag.Entity, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entities[id]
	if !ok {
		return nil, &lightrag.HTTPError{Status: 404, Path: "/entities/" + id, Body: "not found"}
	}
	return &e, nil
}

// UpdateEntity applies a partial update to an entity.
func (c *Client) UpdateEntity(_ context.Context, id string, upd lightrag.EntityUpdate) (*lightrag.Entity, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entities[id]
	if !ok {
		return nil, &lightrag.HTTPError{Status: 404, Path: "/entities/" + id, Body: "not found"}
	}
	if upd.Name != nil {
		e.Name = *upd.Name
	}
	if upd.Type != nil {
		e.Type = *upd.Type
	}
	if upd.Description != nil {
		e.Description = *upd.Description
	}
	if upd.Properties != nil {
		e.Properties = upd.Properties
	}
	c.entities[id] = e
	return &e, nil
}

// ListEdges returns all edges.
func (c *Client) ListEdges(_ context.Context) ([]lightrag.Edge, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]lightrag.Edge, 0, len(c.edges))
	for _, e := range c.edges {
		out = append(out, e)
	}
	return out, nil
}

// CreateEdge stores a new edge.
func (c *Client) CreateEdge(_ context.Context, e lightrag.Edge) (*lightrag.Edge, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e.ID == "" {
		e.ID = c.nextStr("edge")
	}
	c.edges[e.ID] = e
	return &e, nil
}

// DeleteEdge removes an edge by ID.
func (c *Client) DeleteEdge(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.edges[id]; !ok {
		return &lightrag.HTTPError{Status: 404, Path: "/edges/" + id, Body: "not found"}
	}
	delete(c.edges, id)
	return nil
}

// Health always returns nil.
func (c *Client) Health(_ context.Context) error { return nil }

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
