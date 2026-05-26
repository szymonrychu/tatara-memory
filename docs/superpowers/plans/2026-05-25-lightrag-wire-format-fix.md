# tatara-memory v0.1.1 - LightRAG wire-format fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the invented LightRAG wire format in `internal/lightrag/` with the real v1.4.16 schemas so v0.1.1 passes the Wave 7 smoke test end-to-end.

**Architecture:** No structural changes - same `Client` interface, same `internal/memory` adapter pattern. Only the wire-format mapping (types.go, http.go, fake.go, http_test.go) and the corresponding domain translation (translate.go) change.

**Tech Stack:** unchanged - Go 1.25, stdlib net/http + chi, prometheus/client_golang, slog.

**Specs:**
- This impl: `docs/superpowers/specs/2026-05-25-lightrag-wire-format-fix.md`
- Real LightRAG API: `docs/lightrag-openapi-v1.4.16.json` (live capture from running pod)
- Parent design: `~/Documents/tatara/docs/superpowers/specs/2026-05-24-tatara-phases-0-1-design.md`

---

## Wave 1 - Discovery and contract pinning (single subagent)

### Task 1.1: Confirm cluster state and re-capture OpenAPI

**Files:**
- Read: `docs/lightrag-openapi-v1.4.16.json` (already in repo)

- [ ] **Step 1: Verify deployment is live**

```bash
kubectl -n tatara get pods
```

Expected: 6/6 Running (postgres x3, neo4j, lightrag, tatara-memory).

- [ ] **Step 2: Re-capture OpenAPI in case lightrag was upgraded**

```bash
kubectl -n tatara port-forward svc/tatara-memory-lightrag 19621:9621 >/dev/null 2>&1 &
sleep 3
curl -s http://localhost:19621/openapi.json > docs/lightrag-openapi-v1.4.16.json
kill %1
```

- [ ] **Step 3: Extract canonical schemas for each method we use**

For each schema name in the table below, run:

```bash
jq '.components.schemas.<SchemaName>' docs/lightrag-openapi-v1.4.16.json
```

Schemas to extract:
- `InsertTextRequest`, `InsertTextsRequest`, `InsertResponse`
- `QueryRequest`, `QueryResponse`
- `DocsStatusesResponse`, `DocStatusResponse`
- `DeleteDocByIdRequest`, `DeleteDocByIdResponse`
- `EntityCreateRequest`, `EntityUpdateRequest`, `EntityExistsRequest`
- `RelationCreateRequest`, `RelationEditRequest`
- `DeleteEntityRequest`, `DeleteRelationRequest`
- `HealthResponse`

Save each output to a temp file or note in MEMORY for the rest of the wave.

- [ ] **Step 4: No commit needed** (research only).

### Task 1.2: Update types.go to match real LightRAG schemas

**Files:**
- Modify: `internal/lightrag/types.go`

- [ ] **Step 1: Replace `InsertRequest` and `Document`**

The current `InsertRequest{Documents []Document}` does not exist in LightRAG. Replace with shape that matches `InsertTextRequest` (single doc) and add `InsertTextsRequest` (bulk):

```go
// InsertTextRequest is the v1.4 LightRAG body for POST /documents/text.
type InsertTextRequest struct {
    Text string `json:"text"`
    // Description is optional context (LightRAG metadata-like).
    Description string `json:"description,omitempty"`
}

// InsertTextsRequest is the body for POST /documents/texts (bulk).
type InsertTextsRequest struct {
    Texts []string `json:"texts"`
}

// InsertResponse is the actual LightRAG response. The async-ingest model
// returns a status + track_id (background task), NOT a list of doc ids.
type InsertResponse struct {
    Status  string `json:"status"`
    Message string `json:"message,omitempty"`
    TrackID string `json:"track_id,omitempty"`
}
```

Keep `Document` only if used elsewhere (likely just as a domain notion; check). Delete if not.

- [ ] **Step 2: Replace `QueryRequest`/`QueryResponse`**

Match what is in `components.schemas.QueryRequest` exactly. Likely fields:

```go
type QueryRequest struct {
    Query string    `json:"query"`
    Mode  QueryMode `json:"mode"`           // "hybrid"|"local"|"global"|"naive"|"mix"
    OnlyNeedContext  bool `json:"only_need_context,omitempty"`
    OnlyNeedPrompt   bool `json:"only_need_prompt,omitempty"`
    ResponseType     string `json:"response_type,omitempty"`
    TopK             int   `json:"top_k,omitempty"`
    MaxTokenForTextUnit  int `json:"max_token_for_text_unit,omitempty"`
    // ...verify against openapi
}

type QueryResponse struct {
    Response string `json:"response"`
    // verify per openapi
}
```

Verify the field list against the OpenAPI schema and tweak as needed.

- [ ] **Step 3: Replace Entity/Edge request types**

Map our existing `Entity`, `Edge`, `EntityUpdate` to LightRAG's `EntityCreateRequest`/`EntityUpdateRequest`/`RelationCreateRequest`/`RelationEditRequest`. Keep field json tags exactly as the OpenAPI says (LightRAG uses snake_case like `entity_id`, `entity_type`, `source_id`, `target_id`).

- [ ] **Step 4: Add `DeleteDocByIdRequest`, `DeleteEntityRequest`, `DeleteRelationRequest`**

These are required by the new POST-based delete endpoints (LightRAG does not use DELETE verb).

- [ ] **Step 5: Verify compile**

```bash
go build ./internal/lightrag/...
```

Expected: PASS. Many call sites will be broken; ignore for now (Task 1.3 fixes them).

- [ ] **Step 6: Commit**

```bash
git add internal/lightrag/types.go
git commit -m "refactor(lightrag): replace invented types with real v1.4.16 schemas"
```

### Task 1.3: Rewrite http.go method bodies to match real endpoints

**Files:**
- Modify: `internal/lightrag/http.go`

- [ ] **Step 1: InsertDocument**

```go
func (c *HTTPClient) InsertDocument(ctx context.Context, req InsertTextRequest) (*InsertResponse, error) {
    body, err := encodeJSON(req)
    if err != nil { return nil, err }
    var out InsertResponse
    if err := c.do(ctx, OpInsertDocument, http.MethodPost, "/documents/text", body, &out); err != nil {
        return nil, err
    }
    return &out, nil
}
```

- [ ] **Step 2: BulkInsert (new method, replaces the misshapen InsertRequest)**

```go
func (c *HTTPClient) BulkInsertDocuments(ctx context.Context, req InsertTextsRequest) (*InsertResponse, error) {
    body, err := encodeJSON(req)
    if err != nil { return nil, err }
    var out InsertResponse
    if err := c.do(ctx, OpInsertDocumentsBulk, http.MethodPost, "/documents/texts", body, &out); err != nil {
        return nil, err
    }
    return &out, nil
}
```

- [ ] **Step 3: DeleteDocument via POST**

```go
func (c *HTTPClient) DeleteDocument(ctx context.Context, id string) error {
    body, err := encodeJSON(DeleteDocByIdRequest{DocIDs: []string{id}})
    if err != nil { return err }
    return c.do(ctx, OpDeleteDocument, http.MethodPost, "/documents/delete_document", body, nil)
}
```

- [ ] **Step 4: GetDocument via status endpoint**

LightRAG does not expose `GET /documents/{id}` directly. Options:
- Use `/documents/track_status/{track_id}` if we tracked the insert via track_id
- Use `/documents/paginated` with id filter
- Drop GetDocument from the Client and skip the corresponding /v1/memories/{id} GET handler

Pick the simplest. If keeping GetDocument, define it as:

```go
func (c *HTTPClient) GetDocument(ctx context.Context, trackID string) (*DocStatusResponse, error) {
    var out DocStatusResponse
    if err := c.do(ctx, OpGetDocument, http.MethodGet, "/documents/track_status/"+url.PathEscape(trackID), nil, &out); err != nil {
        return nil, err
    }
    return &out, nil
}
```

This changes the semantics of `id` from "document id" to "track id" - propagate through `memory.Service` and confirm the change is acceptable in the parent spec's `/v1/memories/{id}` contract. **Discuss with the user before committing if uncertain.**

- [ ] **Step 5: Query and QueryDescribe**

```go
func (c *HTTPClient) Query(ctx context.Context, req QueryRequest) (*QueryResponse, error) {
    body, err := encodeJSON(req)
    if err != nil { return nil, err }
    var out QueryResponse
    if err := c.do(ctx, OpQuery, http.MethodPost, "/query", body, &out); err != nil {
        return nil, err
    }
    return &out, nil
}

func (c *HTTPClient) QueryDescribe(ctx context.Context, req QueryRequest) (*QueryResponse, error) {
    req.OnlyNeedPrompt = true
    return c.Query(ctx, req)
}
```

(Or use `/query/data` if that endpoint returns the structured form we want - check OpenAPI.)

- [ ] **Step 6: Entity ops**

```go
func (c *HTTPClient) ListEntities(ctx context.Context, q string) ([]Entity, error) {
    // /graph/label/search or /graph/label/list - confirm which fits
    ...
}
func (c *HTTPClient) GetEntity(ctx context.Context, id string) (*Entity, error) {
    body, _ := encodeJSON(EntityExistsRequest{EntityID: id})
    ...
}
func (c *HTTPClient) UpdateEntity(ctx context.Context, id string, upd EntityUpdateRequest) (*Entity, error) {
    body, _ := encodeJSON(upd)
    return c.doAndDecode(... "/graph/entity/edit" ...)
}
```

- [ ] **Step 7: Edge ops**

Map ListEdges -> `/graphs` (returns whole graph), CreateEdge -> `/graph/relation/create`, DeleteEdge -> `/documents/delete_relation`.

- [ ] **Step 8: Health unchanged** (already `GET /health`).

- [ ] **Step 9: Build**

```bash
go build ./internal/lightrag/...
```

Expected: PASS. Tests will fail until Task 1.4.

- [ ] **Step 10: Commit**

```bash
git add internal/lightrag/http.go
git commit -m "refactor(lightrag): rewrite HTTPClient methods to match real LightRAG v1.4.16 API"
```

### Task 1.4: Update http_test.go canned responses

**Files:**
- Modify: `internal/lightrag/http_test.go`

- [ ] **Step 1: For each test, update the httptest handler's canned response to match the real LightRAG schema**

```go
mux.HandleFunc("POST /documents/text", func(w http.ResponseWriter, r *http.Request) {
    // Assert the request body matches what LightRAG expects:
    var req InsertTextRequest
    require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
    require.NotEmpty(t, req.Text, "InsertTextRequest must carry text field")
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(InsertResponse{
        Status:  "success",
        TrackID: "track_abc",
    })
})
```

The strong assertion on `req.Text` is critical - it would have caught the v0.1.0 bug.

- [ ] **Step 2: Same pattern for /query, /graph/entity/edit, /graph/relation/create, /documents/delete_document, /documents/delete_relation, /health**

- [ ] **Step 3: Run tests**

```bash
go test ./internal/lightrag/... -race -count=1 -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/lightrag/http_test.go
git commit -m "test(lightrag): assert real LightRAG schemas in canned responses"
```

### Task 1.5: Update fake.go for the new types

**Files:**
- Modify: `internal/lightrag/fake/fake.go`
- Modify: `internal/lightrag/fake/fake_test.go`

- [ ] **Step 1: Change fake to return canned bodies in the new shape (TrackID for InsertDocument, etc.)**

- [ ] **Step 2: Re-run tests**

```bash
go test ./internal/lightrag/... -race
```

- [ ] **Step 3: Commit**

```bash
git add internal/lightrag/fake/
git commit -m "test(lightrag/fake): match real LightRAG response shapes"
```

### Task 1.6: Update internal/memory translation layer

**Files:**
- Modify: `internal/memory/translate.go`
- Modify: `internal/memory/service.go`
- Modify: `internal/memory/service_test.go`

- [ ] **Step 1: Update `toLightragInsert` and `fromLightragInsert`**

The domain `Memory{ID,Text,Metadata,CreatedAt}` maps to the real LightRAG insert:
- Domain `Text` -> LightRAG `text`
- Domain `Metadata` flat -> LightRAG `description` (json-encoded if needed, or pick a single canonical field)
- Domain `ID` -> NOT sent to LightRAG (LightRAG generates the doc id; we store the LightRAG track_id as our `id`)

- [ ] **Step 2: Update Query / Describe translations**

Map domain `Query{Mode,Text,TopK}` to `lightrag.QueryRequest{Query,Mode,TopK}` (note field rename text -> query).

For QueryResult: domain `QueryResult{Matches []QueryMatch}` needs to be built from LightRAG's `QueryResponse` (likely `response` string + sources list). Adapt.

- [ ] **Step 3: Entity/Edge translations**

Update for the new request shapes.

- [ ] **Step 4: Run memory tests**

```bash
go test ./internal/memory/... -race
```

Some test fixtures will need updating since they reference the old lightrag types.

- [ ] **Step 5: Commit**

```bash
git add internal/memory/
git commit -m "refactor(memory): adapt to new lightrag wire format"
```

### Task 1.7: Add real integration test

**Files:**
- Create: `internal/lightrag/integration_test.go`

- [ ] **Step 1: Write `//go:build integration` test that hits a live LightRAG**

```go
//go:build integration

package lightrag_test

import (
    "context"
    "os"
    "testing"

    "github.com/stretchr/testify/require"
    "github.com/szymonrychu/tatara-memory/internal/lightrag"
)

func TestIntegration_InsertQueryDelete(t *testing.T) {
    baseURL := os.Getenv("LIGHTRAG_BASE_URL")
    if baseURL == "" {
        t.Skip("LIGHTRAG_BASE_URL not set")
    }
    c := lightrag.NewHTTPClient(lightrag.HTTPConfig{BaseURL: baseURL, Registry: prometheus.NewRegistry()})
    ctx := context.Background()

    ins, err := c.InsertDocument(ctx, lightrag.InsertTextRequest{Text: "Smoke test text."})
    require.NoError(t, err)
    require.NotEmpty(t, ins.TrackID)

    // Poll until lightrag has processed the insert; query
    q, err := c.Query(ctx, lightrag.QueryRequest{Query: "smoke", Mode: lightrag.QueryHybrid, TopK: 3})
    require.NoError(t, err)
    require.NotEmpty(t, q.Response)
}
```

- [ ] **Step 2: Run against the port-forwarded lightrag**

```bash
kubectl -n tatara port-forward svc/tatara-memory-lightrag 19621:9621 >/dev/null 2>&1 &
sleep 3
LIGHTRAG_BASE_URL=http://localhost:19621 go test -tags=integration ./internal/lightrag/... -v
kill %1
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/lightrag/integration_test.go
git commit -m "test(lightrag): add integration test against real LightRAG"
```

## Wave 2 - Redeploy + smoke test

### Task 2.1: Build, push, redeploy

- [ ] **Step 1: Bump VERSION**

```bash
cd ~/Documents/tatara/tatara-memory
git tag v0.1.1
```

- [ ] **Step 2: Build and push amd64 image**

```bash
VERSION=0.1.1 make image push
```

- [ ] **Step 3: Update chart image tag (or use --set)**

Edit `charts/tatara-memory/values.yaml`: bump `appVersion` in Chart.yaml or set tag explicitly. Then:

```bash
mise exec -- helm -n tatara upgrade tatara-memory ./charts/tatara-memory \
  --values <(mise exec -- sops -d values/tatara-memory/default.secrets.yaml) \
  --set image.tag=0.1.1
```

- [ ] **Step 4: Wait for rollout**

```bash
kubectl -n tatara rollout status deploy/tatara-memory --timeout=120s
```

### Task 2.2: Full smoke test

Same script as Wave 7 in the original Phase 1 plan (`docs/superpowers/plans/2026-05-24-tatara-memory-phase1.md` Task 7.5). All 5 endpoints (POST, GET, Query, Bulk, DELETE) must succeed.

- [ ] **Step 1: Mint bearer token**

```bash
set -a && source ~/Documents/infra/terraform/keycloak/.env && set +a
CLIENT_SECRET=$(cd ~/Documents/infra/terraform/keycloak && terraform output -raw tatara_memory_client_secret)
TOKEN=$(curl -s -X POST "https://auth.szymonrichert.pl/realms/master/protocol/openid-connect/token" \
  -d "client_id=tatara-memory" -d "client_secret=$CLIENT_SECRET" \
  -d "grant_type=client_credentials" -d "scope=tatara" | jq -r .access_token)
```

- [ ] **Step 2: Port-forward and run all 5 calls**

```bash
kubectl -n tatara port-forward svc/tatara-memory 18080:8080 >/dev/null 2>&1 &
PF=$!
sleep 3

# POST
MEMID=$(curl -sX POST http://localhost:18080/v1/memories \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"text":"Tatara is a Japanese clay furnace for smelting iron sand.","idempotency_key":"smoke-001"}' \
  | jq -r .id)
echo "POST id=$MEMID"

# GET
curl -s http://localhost:18080/v1/memories/$MEMID -H "Authorization: Bearer $TOKEN"

# Query
curl -sX POST http://localhost:18080/v1/queries -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"text":"What is tatara?","mode":"hybrid"}' | jq .

# Bulk
JOB=$(curl -sX POST http://localhost:18080/v1/memories:bulk -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"items":[{"text":"Iron sand is collected.","idempotency_key":"bulk-1"},{"text":"Tamahagane is high-carbon steel.","idempotency_key":"bulk-2"}]}' \
  | jq -r .job_id)
for i in {1..30}; do
  STATUS=$(curl -s http://localhost:18080/v1/ingest-jobs/$JOB -H "Authorization: Bearer $TOKEN" | jq -r .status)
  echo "[$i] $STATUS"
  case "$STATUS" in succeeded|failed|partial) break ;; esac
  sleep 2
done

# DELETE
curl -sX DELETE http://localhost:18080/v1/memories/$MEMID -H "Authorization: Bearer $TOKEN" -o /dev/null -w "%{http_code}\n"

kill $PF
```

Expected: 201, 200, 200, 202+terminal status, 204.

- [ ] **Step 3: Verify Prometheus**

```bash
# Check that http_requests_total and lightrag_calls_total metrics increment per call
kubectl -n tatara port-forward svc/tatara-memory 18080:8080 >/dev/null 2>&1 &
sleep 3
curl -s http://localhost:18080/metrics | grep -E "http_requests_total|lightrag_calls_total|ingest_jobs_total" | head -10
kill %1
```

### Task 2.3: Mark Phase 1 shipped

**Files:**
- Modify: `~/Documents/tatara/ROADMAP.md`
- Modify: `~/Documents/tatara/MEMORY.md`

- [ ] **Step 1: Bump tatara/ROADMAP.md Phase 1 status to `shipped 2026-05-25`**

- [ ] **Step 2: Append to tatara/MEMORY.md**

```
- 2026-05-25 - Phase 1 shipped (tatara-memory v0.1.1). Initial deploy in v0.1.0
  surfaced 3 layers of lightrag wire-format bugs (Wave 2C invented schemas);
  v0.1.1 rewrote the lightrag client against real OpenAPI v1.4.16. Lesson:
  unit tests with httptest must assert request bodies match the real upstream
  schema, not just our own invented one.
```

- [ ] **Step 3: Commit parent docs**

```bash
cd ~/Documents/tatara
git add ROADMAP.md MEMORY.md
git commit -m "docs: mark phase 1 (tatara-memory) shipped at v0.1.1"
```

- [ ] **Step 4: Tag and push tatara-memory**

```bash
cd ~/Documents/tatara/tatara-memory
git push origin main --tags
```

Phase 1 is shipped.

## Self-review for the next agent

Before declaring DONE:

- [ ] `make all` clean (lint, test, build) on main
- [ ] `make chart-lint` and `make chart-test` clean
- [ ] All 5 smoke-test endpoints return their expected status codes
- [ ] Image deployed in cluster matches HEAD commit digest
- [ ] Parent `tatara/ROADMAP.md` updated
- [ ] Parent `tatara/MEMORY.md` updated
- [ ] `v0.1.1` git tag pushed to origin
