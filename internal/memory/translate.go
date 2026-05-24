package memory

import "github.com/szymonrychu/tatara-memory/internal/lightrag"

// ToLightragInsert converts a Memory into a LightRAG InsertRequest.
func ToLightragInsert(m Memory) lightrag.InsertRequest {
	return lightrag.InsertRequest{
		Documents: []lightrag.Document{
			{ID: m.ID, Content: m.Text, Metadata: m.Metadata},
		},
	}
}

// FromLightragQuery converts a LightRAG QueryResponse to a domain QueryResult.
func FromLightragQuery(r lightrag.QueryResponse) QueryResult {
	out := QueryResult{Matches: make([]QueryMatch, 0, len(r.Matches))}
	for _, m := range r.Matches {
		out.Matches = append(out.Matches, QueryMatch{ID: m.ID, Score: m.Score, Text: m.Text})
	}
	return out
}

// FromLightragEntity converts a LightRAG Entity to a domain Entity.
func FromLightragEntity(e lightrag.Entity) Entity {
	return Entity{
		ID:          e.ID,
		Name:        e.Name,
		Type:        e.Type,
		Description: e.Description,
		Properties:  e.Properties,
	}
}

// ToLightragEntityUpdate converts a domain Entity patch to a LightRAG EntityUpdate.
func ToLightragEntityUpdate(e Entity) lightrag.EntityUpdate {
	var name, typ, desc *string
	if e.Name != "" {
		v := e.Name
		name = &v
	}
	if e.Type != "" {
		v := e.Type
		typ = &v
	}
	if e.Description != "" {
		v := e.Description
		desc = &v
	}
	return lightrag.EntityUpdate{
		Name:        name,
		Type:        typ,
		Description: desc,
		Properties:  e.Properties,
	}
}

// FromLightragEdge converts a LightRAG Edge to a domain Edge.
func FromLightragEdge(e lightrag.Edge) Edge {
	return Edge{
		ID:         e.ID,
		From:       e.FromEntity,
		To:         e.ToEntity,
		Relation:   e.Relation,
		Properties: e.Properties,
	}
}

// ToLightragEdge converts a domain Edge to a LightRAG Edge for creation.
func ToLightragEdge(e Edge) lightrag.Edge {
	return lightrag.Edge{
		ID:         e.ID,
		FromEntity: e.From,
		ToEntity:   e.To,
		Relation:   e.Relation,
		Properties: e.Properties,
	}
}
