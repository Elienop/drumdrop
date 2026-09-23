package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/elienop/drumdrop/internal/musora"
)

// stubInstructorSanity points the musora query endpoint at a server that
// answers one instructor, and counts the requests it gets.
func stubInstructorSanity(t *testing.T) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		_, _ = w.Write([]byte(`{"result":[{"_id":"abc","name":"Jared Falk"}]}`))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(musora.SetSanityBase(srv.URL))
	return &n
}

// TestFollowAtNameFollowsTheSlug (ruling #70) drives the CLI's own parse and
// follow path: "drumdrop follow @Jared-Falk", a quoted name, --instructor and
// a coach-page link all follow jared-falk, in the link's brand when no
// --brand is given, exactly as the web's add does.
func TestFollowAtNameFollowsTheSlug(t *testing.T) {
	for _, c := range []struct {
		argv      []string
		wantBrand string
	}{
		{[]string{"@Jared-Falk"}, "drumeo"},
		{[]string{"@Jared Falk", "--brand", "pianote"}, "pianote"},
		{[]string{"--instructor", "JARED FALK"}, "drumeo"},
		{[]string{"@https://app.musora.com/singeo/coaches/jared-falk/314120"}, "singeo"},
	} {
		t.Run(c.argv[0], func(t *testing.T) {
			stubInstructorSanity(t)
			store := newServeTestStore(t)
			args, err := parseFollowArgs(c.argv)
			if err != nil {
				t.Fatal(err)
			}
			input, ok := instructorInput(args)
			if !ok {
				t.Fatalf("%v wasn't taken for an instructor follow", c.argv)
			}
			if err := followInstructor(t.Context(), store, input, args.brand, args.quality); err != nil {
				t.Fatalf("followInstructor: %v", err)
			}
			follows, err := store.ListFollows(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(follows) != 1 || follows[0].Slug.String != "jared-falk" || follows[0].Brand != c.wantBrand {
				t.Fatalf("follows = %+v, want one of jared-falk in %s", follows, c.wantBrand)
			}
		})
	}
}

// TestFollowRefusesWhatCantBeNormalised proves the CLI refuses what the web
// refuses, before Musora is asked and before anything is stored; a bare "@"
// is an instructor follow with nothing typed, not a node follow of "@".
func TestFollowRefusesWhatCantBeNormalised(t *testing.T) {
	for _, argv := range [][]string{
		{"@"},
		{"@jared_falk"},
		{"@https://app.musora.com/drumeo/lessons/course/409875/409875"},
		{"--instructor", "a\nb"},
	} {
		t.Run(argv[len(argv)-1], func(t *testing.T) {
			calls := stubInstructorSanity(t)
			store := newServeTestStore(t)
			args, err := parseFollowArgs(argv)
			if err != nil {
				t.Fatal(err)
			}
			input, ok := instructorInput(args)
			if !ok {
				t.Fatalf("%v wasn't taken for an instructor follow", argv)
			}
			err = followInstructor(t.Context(), store, input, args.brand, args.quality)
			if !errors.Is(err, musora.ErrBadSlug) {
				t.Errorf("err = %v, want ErrBadSlug", err)
			}
			follows, _ := store.ListFollows(t.Context())
			if n := calls.Load(); n != 0 || len(follows) != 0 {
				t.Errorf("Musora asked %d times, %d follows stored; want neither", n, len(follows))
			}
		})
	}
}
