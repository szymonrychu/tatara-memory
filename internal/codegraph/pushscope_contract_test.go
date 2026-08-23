package codegraph_test

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

// This file is the forcing function for the push-scope contract documented on
// Service.Push: every row kind in a GraphPush that names its own owning file
// MUST have that file checked against p.Files, or the row lands outside the
// scope PGStore.Reconcile purges and is unreachable by any later reconcile of
// the files the push actually owned.
//
// It landed once already: Entity, Edge and Symbol were guarded and Hyperedge
// was not (tatara-memory#111), while the test suite carried one hand-written
// rejection test per guarded kind and therefore mirrored the gap exactly.
// TestPushScopeContract_CoversEveryFileOwningKind derives the kind set by
// reflection over GraphPush, so a FIFTH file-owning kind added to the struct
// with no row here fails CI even though every existing row passes. That is the
// property a fourth hand-written per-kind test cannot have.
//
// KNOW THE BOUND. The table is total over ROW KINDS, not over the guards inside
// one kind. It drives exactly one out-of-scope fixture per kind, so a second
// rule on a kind that already has a row is invisible to it: Entity's fileless
// carve-out, Hyperedge's empty-SrcFile rejection, Hyperedge's arity floor and
// Symbol's role check are all separately tested in service_test.go and none of
// them would be missed here. It shortens the list of ways this class can land;
// it does not close it.

// owningFileFields are the struct field names that make a row kind file-owning.
// A GraphPush slice whose element type carries one of these owns a file and must
// therefore be scope-checked.
var owningFileFields = []string{"SrcFile", "FilePath"}

// fileOwningKinds returns the GraphPush field names of every slice whose element
// struct carries an owning-file field. This is derived, never hand-listed: that
// is the whole point of the table.
func fileOwningKinds() []string {
	var out []string
	rt := reflect.TypeOf(codegraph.GraphPush{})
	for i := range rt.NumField() {
		f := rt.Field(i)
		if f.Type.Kind() != reflect.Slice || f.Type.Elem().Kind() != reflect.Struct {
			continue
		}
		for _, name := range owningFileFields {
			if _, ok := f.Type.Elem().FieldByName(name); ok {
				out = append(out, f.Name)
				break
			}
		}
	}
	return out
}

// pushScopeCase drives Push with a GraphPush whose sole row of one kind names a
// file absent from Files. Every other field of the row is valid, so the only
// reason the push can fail is the scope guard under test.
type pushScopeCase struct {
	// kind is the GraphPush field name this row covers.
	kind string
	// push carries exactly one row of that kind, owning "b.go", with Files=["a.go"].
	push codegraph.GraphPush
}

func pushScopeCases() []pushScopeCase {
	return []pushScopeCase{
		{
			kind: "Entities",
			push: codegraph.GraphPush{
				Repo:     "r",
				Files:    []string{"a.go"},
				Entities: []codegraph.Entity{{ID: "e1", Name: "E", Type: "go_func", FilePath: "b.go"}},
			},
		},
		{
			kind: "Edges",
			push: codegraph.GraphPush{
				Repo:  "r",
				Files: []string{"a.go"},
				Edges: []codegraph.Edge{{From: "e1", To: "e2", Relation: "calls", SrcFile: "b.go"}},
			},
		},
		{
			kind: "Symbols",
			push: codegraph.GraphPush{
				Repo:  "r",
				Files: []string{"a.go"},
				Symbols: []codegraph.SymbolRow{
					{Symbol: "Foo", Lang: "go", Kind: "func", Role: codegraph.RoleProvides, EntityID: "e1", SrcFile: "b.go"},
				},
			},
		},
		{
			kind: "Hyperedges",
			push: codegraph.GraphPush{
				Repo:  "r",
				Files: []string{"a.go"},
				Hyperedges: []codegraph.Hyperedge{
					{ID: "h1", Label: "trio", Relation: "form", SrcFile: "b.go",
						Members: []string{"e1", "e2", "e3"}},
				},
			},
		},
	}
}

// TestPushScopeContract_CoversEveryFileOwningKind asserts the table names EXACTLY
// the file-owning kinds GraphPush declares, so adding a kind without thinking
// about its scope guard fails here even if every other row is green.
func TestPushScopeContract_CoversEveryFileOwningKind(t *testing.T) {
	declared := fileOwningKinds()
	var covered []string
	for _, c := range pushScopeCases() {
		covered = append(covered, c.kind)
	}
	sort.Strings(declared)
	sort.Strings(covered)
	require.Equal(t, declared, covered,
		"every GraphPush slice whose rows name an owning file needs a scope-guard row: a row "+
			"naming a file outside Files is written but never purged, because Reconcile scopes "+
			"its deletes by exactly that field")
}

// TestPushScopeContract_RejectsRowOutsideFiles drives each row.
func TestPushScopeContract_RejectsRowOutsideFiles(t *testing.T) {
	for _, c := range pushScopeCases() {
		t.Run(c.kind, func(t *testing.T) {
			svc, fs := newSvc()
			_, err := svc.Push(context.Background(), c.push)
			require.ErrorIs(t, err, codegraph.ErrInvalidScope,
				"%s: a row owning a file outside Files must be rejected, not reconciled", c.kind)
			require.Empty(t, fs.pushed.Repo, "%s: the rejected push must not reach the store", c.kind)
		})
	}
}
