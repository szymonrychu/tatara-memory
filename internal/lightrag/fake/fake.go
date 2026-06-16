// Package fake provides an in-memory implementation of lightrag.Client for use in tests.
package fake

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

// docState holds a doc plus its track_id wiring.
type docState struct {
	doc     lightrag.DocStatusResponse
	trackID string
}

// Client is an in-memory implementation of lightrag.Client.
type Client struct {
	mu       sync.Mutex
	docs     map[string]docState       // doc_id -> state
	tracks   map[string][]string       // track_id -> []doc_id
	entities map[string]map[string]any // entity_name -> entity_data
	edges    map[string]map[string]any // "src||tgt" -> relation_data
	labels   []string                  // for /graph/label/search
	queryRes lightrag.QueryResponse
	dataRes  lightrag.QueryDataResponse
	lastReq  lightrag.QueryRequest // most recent Query request, for assertions
	nextID   int

	// insertStatus controls what DocStatus InsertText assigns to new documents.
	// Defaults to DocStatusProcessed (eager). Set to DocStatusPending or
	// DocStatusProcessing via SetInsertStatus to exercise the polling lifecycle.
	insertStatus lightrag.DocStatus

	// insertRespStatus and insertRespTrackID override the InsertResponse returned
	// by InsertText when insertRespOverride is true. Used to simulate logical
	// failures (non-success status or empty track_id).
	insertRespOverride bool
	insertRespStatus   string
	insertRespTrackID  string

	// leaveEdgesOnDelete disables the fake's eager incident-edge cascade in
	// DeleteEntity, mirroring the real backend's async/eventual behaviour.
	// When true, dangling edges survive the entity deletion.
	leaveEdgesOnDelete bool
}

// New returns a ready-to-use fake Client.
// By default InsertText returns documents in DocStatusProcessed state and
// DeleteEntity eagerly removes incident edges (eager/synchronous semantics).
// Use SetInsertStatus and SetLeaveEdgesOnDelete to exercise async/eventual paths.
func New() *Client {
	return &Client{
		docs:         map[string]docState{},
		tracks:       map[string][]string{},
		entities:     map[string]map[string]any{},
		edges:        map[string]map[string]any{},
		insertStatus: lightrag.DocStatusProcessed,
	}
}

// SetInsertStatus controls the DocStatus assigned to documents created by InsertText.
// Pass DocStatusPending or DocStatusProcessing to exercise polling/lifecycle code paths.
func (c *Client) SetInsertStatus(s lightrag.DocStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.insertStatus = s
}

// SetLeaveEdgesOnDelete controls whether DeleteEntity removes incident edges.
// When true the fake mirrors real LightRAG async behaviour: edges are left
// in place after entity deletion so callers must tolerate dangling references.
func (c *Client) SetLeaveEdgesOnDelete(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.leaveEdgesOnDelete = v
}

// SetInsertResponse overrides the InsertResponse returned by InsertText with a
// fixed status and track_id. Pass status="failure" and trackID="" to simulate
// a logical failure response. Pass status="success" and trackID="" to simulate
// a response with an empty track_id (also a logical error from the service's view).
// Call with override=false equivalent by not calling this method (default: normal).
func (c *Client) SetInsertResponse(status, trackID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.insertRespOverride = true
	c.insertRespStatus = status
	c.insertRespTrackID = trackID
}

func (c *Client) nextStr(prefix string) string {
	c.nextID++
	return prefix + "-" + strconv.Itoa(c.nextID)
}

// SeedEntity pre-populates an entity by name.
func (c *Client) SeedEntity(name string, data map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if data == nil {
		data = map[string]any{}
	}
	c.entities[name] = data
	c.labels = appendUnique(c.labels, name)
}

// SeedEdge pre-populates a relation by (src, tgt).
func (c *Client) SeedEdge(src, tgt string, data map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.edges[edgeKey(src, tgt)] = data
}

// SeedQueryResponse pre-loads the response returned by Query.
func (c *Client) SeedQueryResponse(r lightrag.QueryResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queryRes = r
}

// SeedQueryDataResponse pre-loads the response returned by QueryData.
func (c *Client) SeedQueryDataResponse(r lightrag.QueryDataResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dataRes = r
}

// SeedLabels pre-populates the label index used by LabelSearch.
func (c *Client) SeedLabels(labels []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.labels = append([]string(nil), labels...)
}

// InsertText accepts a text submission and produces a track_id with one doc.
// The doc's initial status is controlled by SetInsertStatus (default: DocStatusProcessed).
// Use SetInsertStatus(DocStatusPending) to exercise polling/lifecycle code paths.
// Use SetInsertResponse to inject a logical-failure response without storing any doc.
func (c *Client) InsertText(_ context.Context, req lightrag.InsertTextRequest) (*lightrag.InsertResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.insertRespOverride {
		return &lightrag.InsertResponse{
			Status:  c.insertRespStatus,
			Message: "override",
			TrackID: c.insertRespTrackID,
		}, nil
	}
	trackID := c.nextStr("track")
	docID := c.nextStr("doc")
	now := time.Now().UTC().Format(time.RFC3339)
	status := c.insertStatus
	if status == "" {
		status = lightrag.DocStatusProcessed
	}
	c.docs[docID] = docState{
		doc: lightrag.DocStatusResponse{
			ID:             docID,
			ContentSummary: req.Text,
			ContentLength:  len(req.Text),
			Status:         status,
			CreatedAt:      now,
			UpdatedAt:      now,
			TrackID:        trackID,
			FilePath:       req.FileSource,
		},
		trackID: trackID,
	}
	c.tracks[trackID] = append(c.tracks[trackID], docID)
	return &lightrag.InsertResponse{
		Status:  "success",
		Message: "submitted",
		TrackID: trackID,
	}, nil
}

// TrackStatus returns the docs associated with trackID.
func (c *Client) TrackStatus(_ context.Context, trackID string) (*lightrag.TrackStatusResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	docIDs, ok := c.tracks[trackID]
	if !ok {
		return nil, &lightrag.HTTPError{Status: 404, Path: "/documents/track_status/" + trackID, Body: "not found"}
	}
	out := lightrag.TrackStatusResponse{
		TrackID:       trackID,
		TotalCount:    len(docIDs),
		StatusSummary: map[string]int{},
	}
	for _, id := range docIDs {
		s := c.docs[id]
		out.Documents = append(out.Documents, s.doc)
		out.StatusSummary[string(s.doc.Status)]++
	}
	return &out, nil
}

// DeleteDocs deletes documents and removes any track references.
// All doc IDs are validated before any mutation so a missing ID never
// leaves the store in a partially-mutated state.
func (c *Client) DeleteDocs(_ context.Context, req lightrag.DeleteDocRequest) (*lightrag.DeleteDocByIdResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range req.DocIDs {
		if _, ok := c.docs[id]; !ok {
			return nil, &lightrag.HTTPError{Status: 404, Path: "/documents/delete_document", Body: "doc not found: " + id}
		}
	}
	for _, id := range req.DocIDs {
		state := c.docs[id]
		delete(c.docs, id)
		if state.trackID != "" {
			ids := c.tracks[state.trackID]
			filtered := make([]string, 0, len(ids))
			for _, did := range ids {
				if did != id {
					filtered = append(filtered, did)
				}
			}
			if len(filtered) == 0 {
				delete(c.tracks, state.trackID)
			} else {
				c.tracks[state.trackID] = filtered
			}
		}
	}
	docID := ""
	if len(req.DocIDs) > 0 {
		docID = req.DocIDs[0]
	}
	return &lightrag.DeleteDocByIdResponse{
		Status:  "deletion_started",
		Message: "deleted",
		DocID:   docID,
	}, nil
}

// LastQuery returns the most recent QueryRequest passed to Query.
func (c *Client) LastQuery() lightrag.QueryRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastReq
}

// Query returns the seeded query response.
func (c *Client) Query(_ context.Context, req lightrag.QueryRequest) (*lightrag.QueryResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastReq = req
	cp := c.queryRes
	if cp.References != nil {
		cp.References = append([]lightrag.ReferenceItem(nil), cp.References...)
	}
	return &cp, nil
}

// QueryData returns the seeded data response.
func (c *Client) QueryData(_ context.Context, _ lightrag.QueryRequest) (*lightrag.QueryDataResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := c.dataRes
	return &cp, nil
}

// EntityExists reports whether an entity by name exists.
func (c *Client) EntityExists(_ context.Context, name string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.entities[name]
	return ok, nil
}

// CreateEntity stores a new entity.
func (c *Client) CreateEntity(_ context.Context, req lightrag.EntityCreateRequest) (*lightrag.EntityResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entities[req.EntityName]; ok {
		return nil, &lightrag.HTTPError{Status: 400, Path: "/graph/entity/create", Body: "duplicate entity"}
	}
	data := req.EntityData
	if data == nil {
		data = map[string]any{}
	}
	data["entity_name"] = req.EntityName
	c.entities[req.EntityName] = data
	c.labels = appendUnique(c.labels, req.EntityName)
	return &lightrag.EntityResponse{Status: "success", Message: "created", Data: copyMap(data)}, nil
}

// UpdateEntity applies a partial update to an existing entity.
func (c *Client) UpdateEntity(_ context.Context, req lightrag.EntityUpdateRequest) (*lightrag.EntityResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.entities[req.EntityName]
	if !ok {
		return nil, &lightrag.HTTPError{Status: 404, Path: "/graph/entity/edit", Body: "not found"}
	}
	finalName := req.EntityName
	if v, hasRename := req.UpdatedData["entity_name"]; hasRename {
		if s, ok := v.(string); ok && s != "" && s != req.EntityName {
			if !req.AllowRename {
				return nil, &lightrag.HTTPError{Status: 400, Path: "/graph/entity/edit", Body: "rename not allowed"}
			}
			finalName = s
		}
	}
	for k, v := range req.UpdatedData {
		if k == "entity_name" {
			continue
		}
		cur[k] = v
	}
	cur["entity_name"] = finalName
	if finalName != req.EntityName {
		delete(c.entities, req.EntityName)
		c.entities[finalName] = cur
		c.labels = renameLabel(c.labels, req.EntityName, finalName)
	}
	return &lightrag.EntityResponse{Status: "success", Message: "updated", Data: copyMap(cur)}, nil
}

// DeleteEntity removes an entity.
// By default (leaveEdgesOnDelete==false) incident edges are also removed for test convenience.
// Set SetLeaveEdgesOnDelete(true) to mirror real LightRAG async semantics where edges may
// survive the deletion and callers must tolerate dangling references.
func (c *Client) DeleteEntity(_ context.Context, req lightrag.DeleteEntityRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entities[req.EntityName]; !ok {
		return &lightrag.HTTPError{Status: 404, Path: "/documents/delete_entity", Body: "not found"}
	}
	delete(c.entities, req.EntityName)
	c.labels = removeLabel(c.labels, req.EntityName)
	if !c.leaveEdgesOnDelete {
		for k := range c.edges {
			src, tgt := parseEdgeKey(k)
			if src == req.EntityName || tgt == req.EntityName {
				delete(c.edges, k)
			}
		}
	}
	return nil
}

// LabelSearch returns labels matching q (case-insensitive substring).
func (c *Client) LabelSearch(_ context.Context, q string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []string{}
	for _, l := range c.labels {
		if q == "" || containsFold(l, q) {
			out = append(out, l)
		}
	}
	return out, nil
}

// Graph returns a subgraph rooted at label.
func (c *Client) Graph(_ context.Context, label string, _, _ int) (*lightrag.KnowledgeGraph, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, ok := c.entities[label]
	if !ok {
		return nil, &lightrag.HTTPError{Status: 404, Path: "/graphs", Body: "label not found"}
	}
	g := &lightrag.KnowledgeGraph{
		Nodes: []lightrag.GraphNode{{ID: label, Labels: []string{label}, Properties: copyMap(data)}},
	}
	for k, props := range c.edges {
		src, tgt := parseEdgeKey(k)
		if src == label || tgt == label {
			g.Edges = append(g.Edges, lightrag.GraphEdge{
				Source: src, Target: tgt, Properties: copyMap(props),
			})
			other := tgt
			if tgt == label {
				other = src
			}
			if d, ok := c.entities[other]; ok {
				g.Nodes = append(g.Nodes, lightrag.GraphNode{ID: other, Labels: []string{other}, Properties: copyMap(d)})
			}
		}
	}
	return g, nil
}

// CreateRelation stores a relation between two existing entities.
func (c *Client) CreateRelation(_ context.Context, req lightrag.RelationCreateRequest) (*lightrag.RelationResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entities[req.SourceEntity]; !ok {
		return nil, &lightrag.HTTPError{Status: 400, Path: "/graph/relation/create", Body: "source not found"}
	}
	if _, ok := c.entities[req.TargetEntity]; !ok {
		return nil, &lightrag.HTTPError{Status: 400, Path: "/graph/relation/create", Body: "target not found"}
	}
	data := req.RelationData
	if data == nil {
		data = map[string]any{}
	}
	data["src_id"] = req.SourceEntity
	data["tgt_id"] = req.TargetEntity
	c.edges[edgeKey(req.SourceEntity, req.TargetEntity)] = data
	return &lightrag.RelationResponse{Status: "success", Message: "created", Data: copyMap(data)}, nil
}

// DeleteRelation removes a relation by (src, tgt).
func (c *Client) DeleteRelation(_ context.Context, req lightrag.DeleteRelationRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := edgeKey(req.SourceEntity, req.TargetEntity)
	if _, ok := c.edges[key]; !ok {
		return &lightrag.HTTPError{Status: 404, Path: "/documents/delete_relation", Body: "not found"}
	}
	delete(c.edges, key)
	return nil
}

// Health always returns nil.
func (c *Client) Health(_ context.Context) error { return nil }

// LookupTracksForDoc returns the trackIDs that include a given doc_id (test helper).
func (c *Client) LookupTracksForDoc(docID string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []string{}
	for tid, docIDs := range c.tracks {
		for _, d := range docIDs {
			if d == docID {
				out = append(out, tid)
			}
		}
	}
	return out
}

func edgeKey(src, tgt string) string { return src + "||" + tgt }

func parseEdgeKey(k string) (string, string) {
	i := strings.Index(k, "||")
	if i < 0 {
		return k, ""
	}
	return k[:i], k[i+2:]
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func removeLabel(s []string, v string) []string {
	out := make([]string, 0, len(s))
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func renameLabel(s []string, from, to string) []string {
	for i, x := range s {
		if x == from {
			s[i] = to
		}
	}
	return s
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

func copyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
