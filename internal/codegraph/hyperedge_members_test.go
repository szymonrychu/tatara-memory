package codegraph_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

// code_hyperedge_members.entity_id is a bare text column: 0003_phase0_graphify.sql
// declares no foreign key to code_entities, so nothing under Reconcile catches a
// member naming no entity. The check therefore lives in Reconcile's own
// transaction, where p.Entities are already inserted and a member the same push
// creates resolves without a second round trip.
//
// These run against a real PGStore through the stub driver.Connector rather than
// under the integration tag, because CI does not run that tag: a guard tested
// only there is a guard no required check ever executes. The integration twins in
// pgstore_test.go cover the SQL itself against a live Postgres.

func hyperedgePush(members ...string) codegraph.GraphPush {
	return codegraph.GraphPush{
		Repo:  "repo/a",
		Files: []string{"a.go"},
		Entities: []codegraph.Entity{
			{ID: "e1", Name: "E1", Type: "go_func", FilePath: "a.go"},
		},
		Hyperedges: []codegraph.Hyperedge{
			{ID: "h1", Label: "trio", Relation: "form", SrcFile: "a.go", Members: members},
		},
	}
}

func TestReconcileRejectsHyperedgeMemberNamingNoEntity(t *testing.T) {
	sdb := newStubDB()
	sdb.missingMembers = []string{"gone"}
	db := openStubDB(t, sdb)
	store := codegraph.NewPGStore(db)

	_, err := store.Reconcile(context.Background(), hyperedgePush("e1", "pre-existing", "gone"))
	require.ErrorIs(t, err, codegraph.ErrInvalidScope)
	require.ErrorContains(t, err, "gone", "the error must name the member that resolved to nothing")
	require.Zero(t, sdb.commits, "a rejected push must roll back, not commit a partial graph")
	requirePoolDrained(t, db)
}

func TestReconcileAcceptsHyperedgeMembersThatResolve(t *testing.T) {
	sdb := newStubDB()
	db := openStubDB(t, sdb)
	store := codegraph.NewPGStore(db)

	_, err := store.Reconcile(context.Background(), hyperedgePush("e1", "pre-existing", "also-pre-existing"))
	require.NoError(t, err)
	require.Equal(t, 1, sdb.commits)
}

// TestReconcileQueriesOnlyMembersAbsentFromThePush pins the two properties that
// keep the check to one round trip: ids the push itself creates are resolved in
// Go against p.Entities, and the remainder are asked about once, deduplicated.
func TestReconcileQueriesOnlyMembersAbsentFromThePush(t *testing.T) {
	sdb := newStubDB()
	db := openStubDB(t, sdb)
	store := codegraph.NewPGStore(db)

	p := hyperedgePush("e1", "outside", "outside", "other")
	_, err := store.Reconcile(context.Background(), p)
	require.NoError(t, err)
	require.Equal(t, []string{"outside", "other"}, sdb.queriedMembers,
		"e1 is in p.Entities so it must not be queried, and a repeated id must be asked about once")
}

// TestReconcileSkipsMemberCheckWhenEveryMemberIsInThePush proves the query is not
// issued at all when nothing needs looking up. queriedMembers stays nil.
func TestReconcileSkipsMemberCheckWhenEveryMemberIsInThePush(t *testing.T) {
	sdb := newStubDB()
	db := openStubDB(t, sdb)
	store := codegraph.NewPGStore(db)

	p := hyperedgePush("e1", "e2", "e3")
	p.Entities = append(p.Entities,
		codegraph.Entity{ID: "e2", Name: "E2", Type: "go_func", FilePath: "a.go"},
		codegraph.Entity{ID: "e3", Name: "E3", Type: "go_func", FilePath: "a.go"},
	)
	_, err := store.Reconcile(context.Background(), p)
	require.NoError(t, err)
	require.Nil(t, sdb.queriedMembers, "every member is in the push, so no existence query is needed")
}
