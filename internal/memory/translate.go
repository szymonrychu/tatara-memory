package memory

import (
	"fmt"
	"log/slog"
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
func FromDocStatus(trackID string, d lightrag.DocStatusResponse) Memory {
	var createdAt time.Time
	if d.CreatedAt != "" {
		var err error
		createdAt, err = time.Parse(time.RFC3339, d.CreatedAt)
		if err != nil {
			// Try RFC3339Nano before giving up.
			createdAt, err = time.Parse(time.RFC3339Nano, d.CreatedAt)
			if err != nil {
				slog.Warn("memory: unparseable created_at", "track_id", trackID, "raw", d.CreatedAt)
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
	}
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
func EntityUpdatePayload(patch Entity) map[string]any {
	data := map[string]any{}
	if patch.Name != "" {
		data["entity_name"] = patch.Name
	}
	if patch.Type != "" {
		data["entity_type"] = patch.Type
	}
	if patch.Description != "" {
		data["description"] = patch.Description
	}
	for k, v := range patch.Properties {
		data[k] = v
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
