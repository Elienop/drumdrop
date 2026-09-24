package scheduler

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/elienop/drumdrop/internal/database"
)

// TestPlanDoesNotRestampALessonTwoFollowsList pins owner ruling 2026-09-24 (r)
// for a lesson two follows list (code round 5f-5g L1): a course follow gives
// it the course as its parent, an instructor follow gives it none, and each
// sync upserts it once per follow. With the real store, only the first sync
// stamps it (the insert); later syncs that find it as it was leave its
// updated_at, and its parent stays the one the follow it is attributed to
// (the first to list it) gives, whichever order the follows run in.
func TestPlanDoesNotRestampALessonTwoFollowsList(t *testing.T) {
	const past = "2026-01-01 00:00:00"
	for _, instructorFirst := range []bool{false, true} {
		name := "the course follow first"
		if instructorFirst {
			name = "the instructor follow first"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db, err := database.Open(filepath.Join(t.TempDir(), "p.db"))
			if err != nil {
				t.Fatal(err)
			}
			if err := database.RunMigrations(db); err != nil {
				t.Fatal(err)
			}
			s := database.NewStore(db)
			t.Cleanup(func() { s.Close() })
			addNode := func() database.Follow {
				f, err := s.AddNodeFollow(ctx, 4242, "Beginner Course", "drumeo", "1080")
				if err != nil {
					t.Fatal(err)
				}
				return f
			}
			addInstructor := func() database.Follow {
				f, err := s.AddInstructorFollow(ctx, "mike-johnston", "Mike Johnston", "drumeo", "720")
				if err != nil {
					t.Fatal(err)
				}
				return f
			}
			var node, inst database.Follow
			if instructorFirst {
				inst, node = addInstructor(), addNode()
			} else {
				node, inst = addNode(), addInstructor()
			}
			wantParent := sql.NullInt64{Int64: 4242, Valid: true}
			if instructorFirst {
				wantParent = sql.NullInt64{}
			}
			lessons := []int{100, 101}
			p := &Planner{Store: s, Expander: fakeExpander{
				ids:    map[int64][]int{node.ID: lessons, inst.ID: lessons},
				titles: map[int]string{100: "Lesson A", 101: "Lesson B"},
			}}

			for sync := 1; sync <= 3; sync++ {
				if _, err := p.Plan(ctx, 0); err != nil {
					t.Fatalf("sync %d: %v", sync, err)
				}
				for _, id := range lessons {
					l, err := s.GetLesson(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
					stamped := !l.UpdatedAt.Valid || l.UpdatedAt.Time.UTC().Format("2006-01-02 15:04:05") != past
					if sync > 1 && stamped {
						t.Errorf("sync %d stamped lesson %d (updated_at %v), which it found as it was", sync, id, l.UpdatedAt)
					}
					if l.ParentRailcontentID != wantParent {
						t.Errorf("sync %d: lesson %d parent = %+v, want %+v (the attributed follow's)", sync, id, l.ParentRailcontentID, wantParent)
					}
				}
				if _, err := db.Exec(`UPDATE lessons SET updated_at = ?`, past); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
