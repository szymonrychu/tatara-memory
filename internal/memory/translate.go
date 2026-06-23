package memory

import (
	"fmt"
	"strings"
	"time"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

// ToInsertText maps a Memory to a LightRAG InsertTextRequest.
func ToInsertText(m Memory) lightrag.InsertTextRequest {
	return lightrag.InsertTextRequest{Text: m.Text}
}

// FromDocStatus maps a LightRAG DocStatusResponse into a domain Memory.
// trackID becomes Memory.ID; ContentSummary becomes Text. LightRAG's
// metadata values are heterogeneous; non-strings are rendered via fmt.Sprint
// into the string-valued domain map.
// A non-empty but unparseable CreatedAt is returned as the second value so the
// caller can log via its context-aware/injectable logger (rule 11/12). A zero
// CreatedAt in the returned Memory signals the parse failed or the field was absent.
func FromDocStatus(trackID string, d lightrag.DocStatusResponse) (Memory, error) {
	var createdAt time.Time
	var parseErr error
	if d.CreatedAt != "" {
		var err error
		createdAt, err = time.Parse(time.RFC3339, d.CreatedAt)
		if err != nil {
			// Try RFC3339Nano before giving up.
			createdAt, err = time.Parse(time.RFC3339Nano, d.CreatedAt)
			if err != nil {
				parseErr = fmt.Errorf("memory: unparseable created_at %q for track %s: %w", d.CreatedAt, trackID, err)
			}
		}
	}
	var md map[string]string
	if len(d.Metadata) > 0 {
		md = make(map[string]string, len(d.Metadata))
		for k, v := range d.Metadata {
			if s, ok := v.(string); ok {
				md[k] = s
				continue
			}
			md[k] = fmt.Sprint(v)
		}
	}
	return Memory{
		ID:        trackID,
		Text:      d.ContentSummary,
		Metadata:  md,
		CreatedAt: createdAt,
	}, parseErr
}

// QueryResultFromQuery maps a LightRAG QueryResponse to a domain QueryResult.
// References become Matches; Score is zero because /query doesn't return ranking.
func QueryResultFromQuery(r lightrag.QueryResponse) QueryResult {
	out := QueryResult{Matches: make([]QueryMatch, 0, len(r.References))}
	for _, ref := range r.References {
		text := ref.FilePath
		if len(ref.Content) > 0 {
			text = strings.Join(ref.Content, "\n")
		}
		out.Matches = append(out.Matches, QueryMatch{
			ID:    ref.ReferenceID,
			Score: 0,
			Text:  text,
		})
	}
	return out
}

// DescribeResultFromQuery maps a LightRAG QueryResponse into a DescribeResult.
// references[].file_path is collected into Sources.
func DescribeResultFromQuery(r lightrag.QueryResponse) DescribeResult {
	sources := make([]string, 0, len(r.References))
	for _, ref := range r.References {
		sources = append(sources, ref.FilePath)
	}
	return DescribeResult{Response: r.Response, Sources: sources}
}

// queryDataChunk is the domain view of one LightRAG v1.4.16 /query/data
// data.chunks[] entry. The confirmed wire shape is
// {content, file_path, chunk_id, reference_id}; there is no per-chunk relevance,
// score, or similarity field (verified against the v1.4.16 source - see
// MEMORY.md 2026-06-23). reference_id is retained for tombstone filtering.
type queryDataChunk struct {
	chunkID     string
	referenceID string
	text        string
}

// chunksFromQueryData reads data.chunks[] from a LightRAG /query/data envelope,
// preserving retrieval order. The envelope is map[string]any (the OpenAPI is too
// lossy to type), so every level is asserted defensively: a missing or
// wrongly-typed chunks key, or a non-object entry, is skipped rather than assumed.
func chunksFromQueryData(r lightrag.QueryDataResponse) []queryDataChunk {
	raw, ok := r.Data["chunks"].([]any)
	if !ok {
		return nil
	}
	chunks := make([]queryDataChunk, 0, len(raw))
	for _, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		content, _ := stringProp(obj, "content")
		chunkID, _ := stringProp(obj, "chunk_id")
		refID, _ := stringProp(obj, "reference_id")
		chunks = append(chunks, queryDataChunk{chunkID: chunkID, referenceID: refID, text: content})
	}
	return chunks
}

// queryResultFromChunks maps ordered /query/data chunks into ranked Matches.
// LightRAG v1.4.16 returns chunks in retrieval order but carries no per-chunk
// relevance score, so Score is derived from retrieval rank as 1/(rank+1): a
// deterministic, strictly-descending signal that reflects the retrieval engine's
// own chunk ordering instead of the reference arrival order /query exposes. ID is
// chunk_id (falling back to reference_id when absent); Text is the chunk content.
func queryResultFromChunks(chunks []queryDataChunk) QueryResult {
	out := QueryResult{Matches: make([]QueryMatch, 0, len(chunks))}
	for i, ch := range chunks {
		id := ch.chunkID
		if id == "" {
			id = ch.referenceID
		}
		out.Matches = append(out.Matches, QueryMatch{
			ID:    id,
			Score: 1.0 / float64(i+1),
			Text:  ch.text,
		})
	}
	return out
}

// QueryResultFromQueryData maps a LightRAG /query/data response into ranked
// domain Matches (see queryResultFromChunks for the scoring rationale).
func QueryResultFromQueryData(r lightrag.QueryDataResponse) QueryResult {
	return queryResultFromChunks(chunksFromQueryData(r))
}

// EntityFromGraphNode maps a graph node into a domain Entity.
// entity_name is taken from node.ID; entity_type / description from properties.
func EntityFromGraphNode(n lightrag.GraphNode) Entity {
	e := Entity{ID: n.ID, Name: n.ID}
	if t, ok := stringProp(n.Properties, "entity_type"); ok {
		e.Type = t
	}
	if d, ok := stringProp(n.Properties, "description"); ok {
		e.Description = d
	}
	if n.Properties != nil {
		props := make(map[string]string, len(n.Properties))
		for k, v := range n.Properties {
			if k == "entity_name" || k == "entity_type" || k == "description" {
				continue
			}
			if s, ok := v.(string); ok {
				props[k] = s
			}
		}
		if len(props) > 0 {
			e.Properties = props
		}
	}
	return e
}

// EntityUpdatePayload builds the LightRAG updated_data dict from a domain Entity patch.
// Properties are copied first; typed fields (Name/Type/Description) are written last
// so they always win over any same-keyed entry in Properties (mirrors the asymmetric
// skip in EntityFromGraphNode on the read side: reserved keys are excluded from Properties).
func EntityUpdatePayload(patch Entity) map[string]any {
	data := map[string]any{}
	for k, v := range patch.Properties {
		if k == "entity_name" || k == "entity_type" || k == "description" {
			continue // reserved keys must not be injected via Properties
		}
		data[k] = v
	}
	if patch.Name != "" {
		data["entity_name"] = patch.Name
	}
	if patch.Type != "" {
		data["entity_type"] = patch.Type
	}
	if patch.Description != "" {
		data["description"] = patch.Description
	}
	return data
}

// EdgeFromGraphEdge maps a graph edge into a domain Edge.
// Relation is read from relation_data["keywords"] as the primary source (symmetric
// with RelationCreatePayload which writes the relation into keywords), with e.Type
// as the fallback for edges created by external tooling.
func EdgeFromGraphEdge(e lightrag.GraphEdge) Edge {
	out := Edge{
		ID:   EncodeEdgeID(e.Source, e.Target),
		From: e.Source,
		To:   e.Target,
	}
	if e.Properties != nil {
		if r, ok := stringProp(e.Properties, "keywords"); ok {
			out.Relation = r
		}
	}
	if out.Relation == "" {
		out.Relation = e.Type
	}
	if e.Properties != nil {
		props := make(map[string]string, len(e.Properties))
		for k, v := range e.Properties {
			if s, ok := v.(string); ok {
				props[k] = s
			}
		}
		if len(props) > 0 {
			out.Properties = props
		}
	}
	return out
}

// RelationCreatePayload turns a domain Edge into the create-relation request body.
func RelationCreatePayload(e Edge) lightrag.RelationCreateRequest {
	data := map[string]any{"keywords": e.Relation}
	for k, v := range e.Properties {
		data[k] = v
	}
	return lightrag.RelationCreateRequest{
		SourceEntity: e.From,
		TargetEntity: e.To,
		RelationData: data,
	}
}

// entityFromUpdateResponse builds a domain Entity from an UpdateEntity response.
// This avoids a second upstream GetEntity call (N+1 / TOCTOU) by reading the
// entity fields directly from the response data map that LightRAG returns.
func entityFromUpdateResponse(resp *lightrag.EntityResponse, fallbackName string) Entity {
	if resp == nil || resp.Data == nil {
		return Entity{ID: fallbackName, Name: fallbackName}
	}
	node := lightrag.GraphNode{ID: fallbackName, Properties: resp.Data}
	// entity_name in the response data takes precedence over fallbackName.
	if n, ok := stringProp(resp.Data, "entity_name"); ok && n != "" {
		node.ID = n
	}
	return EntityFromGraphNode(node)
}

func stringProp(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
