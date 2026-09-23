package main

import (
	"errors"
	"testing"

	"github.com/elienop/drumdrop/internal/musora"
	"github.com/elienop/drumdrop/internal/musora/musoratest"
)

// TestFollowNodeChecksItsBrand proves the CLI refuses a node follow's --brand
// Musora doesn't have, as the web's add does, before Musora is asked and
// before anything is stored: every lesson of the follow is stored with it.
func TestFollowNodeChecksItsBrand(t *testing.T) {
	queries := musoratest.Serve(t, musoratest.JaredFalk())
	store := newServeTestStore(t)
	args, err := parseFollowArgs([]string{"409875", "--brand", "rockstar"})
	if err != nil {
		t.Fatal(err)
	}
	err = followNode(t.Context(), store, args.positionals[0], args.brand, args.quality)
	if !errors.Is(err, musora.ErrBadBrand) {
		t.Errorf("err = %v, want ErrBadBrand", err)
	}
	follows, _ := store.ListFollows(t.Context())
	if n := len(queries()); n != 0 || len(follows) != 0 {
		t.Errorf("Musora asked %d times, %d follows stored; want neither", n, len(follows))
	}
}

// TestFollowRefusesACoachLinkAsANode proves "drumdrop follow <coach link>",
// without @, isn't taken for a node follow of the coach's number: it says how
// to follow the instructor, before Musora is asked and before anything is
// stored.
func TestFollowRefusesACoachLinkAsANode(t *testing.T) {
	queries := musoratest.Serve(t, musoratest.JaredFalk())
	store := newServeTestStore(t)
	args, err := parseFollowArgs([]string{"https://app.musora.com/drumeo/coaches/jared-falk/31880"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := instructorInput(args); ok {
		t.Fatal("a link without @ was taken for an instructor follow")
	}
	err = followNode(t.Context(), store, args.positionals[0], args.brand, args.quality)
	if !errors.Is(err, errCoachLinkAsNode) {
		t.Errorf("err = %v, want errCoachLinkAsNode", err)
	}
	follows, _ := store.ListFollows(t.Context())
	if n := len(queries()); n != 0 || len(follows) != 0 {
		t.Errorf("Musora asked %d times, %d follows stored; want neither", n, len(follows))
	}
}

// TestFollowInstructorStoresTheBrandsName proves the CLI's instructor follow
// stores the name from the instructor document for the follow's brand, when
// Musora files them under one slug in several, the other brand's first.
func TestFollowInstructorStoresTheBrandsName(t *testing.T) {
	for _, c := range []struct {
		argv            []string
		brand, wantName string
	}{
		{[]string{"@jared-falk"}, "drumeo", "Jared Falk"},
		{[]string{"@jared-falk", "--brand", "singeo"}, "singeo", "Jared Falk (Singeo)"},
	} {
		t.Run(c.brand, func(t *testing.T) {
			musoratest.Serve(t, musoratest.JaredFalk())
			store := newServeTestStore(t)
			args, err := parseFollowArgs(c.argv)
			if err != nil {
				t.Fatal(err)
			}
			input, _ := instructorInput(args)
			if err := followInstructor(t.Context(), store, input, args.brand, args.quality); err != nil {
				t.Fatalf("followInstructor: %v", err)
			}
			follows, err := store.ListFollows(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(follows) != 1 || follows[0].Title != c.wantName || follows[0].Brand != c.brand {
				t.Fatalf("follows = %+v, want one titled %q in %s", follows, c.wantName, c.brand)
			}
		})
	}
}
