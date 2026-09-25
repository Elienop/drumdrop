package server

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/elienop/drumdrop/internal/database"
)

// fixedTime is a stable timestamp used across the DTO tests so the marshaled
// JSON is deterministic.
var fixedTime = time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

// jsonField marshals v and decodes it back into a generic map so a test can
// assert on a single top-level JSON field without depending on key order.
func jsonField(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func TestFollowDTO_NullsMarshalAsNull(t *testing.T) {
	f := database.Follow{
		ID:            7,
		Kind:          "instructor",
		RailcontentID: sql.NullInt64{}, // NULL
		Slug:          sql.NullString{String: "jared-falk", Valid: true},
		Title:         "Jared Falk",
		Brand:         "drumeo",
		Quality:       "best",
		AddedAt:       fixedTime,
		LastSyncedAt:  sql.NullTime{}, // NULL
	}

	m := jsonField(t, followDTO(f))

	if got := string(m["railcontent_id"]); got != "null" {
		t.Errorf("railcontent_id = %s, want null", got)
	}
	if got := string(m["last_synced_at"]); got != "null" {
		t.Errorf("last_synced_at = %s, want null", got)
	}
	if got := string(m["slug"]); got != `"jared-falk"` {
		t.Errorf("slug = %s, want \"jared-falk\"", got)
	}
	if got := string(m["id"]); got != "7" {
		t.Errorf("id = %s, want 7", got)
	}
}

func TestFollowDTO_SetValuesMarshalAsScalar(t *testing.T) {
	f := database.Follow{
		ID:            3,
		Kind:          "node",
		RailcontentID: sql.NullInt64{Int64: 12345, Valid: true},
		Slug:          sql.NullString{}, // NULL
		Title:         "Beginner Course",
		Brand:         "drumeo",
		Quality:       "1080p",
		AddedAt:       fixedTime,
		LastSyncedAt:  sql.NullTime{Time: fixedTime, Valid: true},
	}

	m := jsonField(t, followDTO(f))

	if got := string(m["railcontent_id"]); got != "12345" {
		t.Errorf("railcontent_id = %s, want 12345", got)
	}
	if got := string(m["slug"]); got != "null" {
		t.Errorf("slug = %s, want null", got)
	}
	if got := string(m["last_synced_at"]); got != `"2026-05-30T12:00:00Z"` {
		t.Errorf("last_synced_at = %s, want RFC3339 scalar", got)
	}
}

func TestLessonDTO_NullsMarshalAsNull(t *testing.T) {
	l := database.Lesson{
		RailcontentID:       999,
		Title:               "Rudiments",
		ParentRailcontentID: sql.NullInt64{}, // NULL
		Brand:               "drumeo",
		Status:              database.StatusPending,
		Quality:             sql.NullString{}, // NULL
		OutputDir:           sql.NullString{}, // NULL
		VideoPath:           sql.NullString{}, // NULL
		Bytes:               sql.NullInt64{},  // NULL
		Error:               sql.NullString{}, // NULL
		FollowID:            sql.NullInt64{},  // NULL
		FirstSeenAt:         sql.NullTime{},   // NULL
		DownloadedAt:        sql.NullTime{},   // NULL
		UpdatedAt:           sql.NullTime{},   // NULL
	}

	m := jsonField(t, lessonDTO(l))

	for _, key := range []string{
		"parent_railcontent_id", "quality", "output_dir", "video_path",
		"bytes", "error", "follow_id", "first_seen_at", "downloaded_at", "updated_at",
	} {
		if got := string(m[key]); got != "null" {
			t.Errorf("%s = %s, want null", key, got)
		}
	}
	if got := string(m["railcontent_id"]); got != "999" {
		t.Errorf("railcontent_id = %s, want 999", got)
	}
	if got := string(m["status"]); got != `"pending"` {
		t.Errorf("status = %s, want \"pending\"", got)
	}
}

func TestLessonDTO_SetValuesMarshalAsScalar(t *testing.T) {
	l := database.Lesson{
		RailcontentID:       42,
		Title:               "Paradiddles",
		ParentRailcontentID: sql.NullInt64{Int64: 7, Valid: true},
		Brand:               "drumeo",
		Status:              database.StatusDownloaded,
		Quality:             sql.NullString{String: "1080p", Valid: true},
		OutputDir:           sql.NullString{String: "/out", Valid: true},
		VideoPath:           sql.NullString{String: "/out/v.mp4", Valid: true},
		Bytes:               sql.NullInt64{Int64: 1024, Valid: true},
		Error:               sql.NullString{String: "", Valid: true},
		FollowID:            sql.NullInt64{Int64: 3, Valid: true},
		FirstSeenAt:         sql.NullTime{Time: fixedTime, Valid: true},
		DownloadedAt:        sql.NullTime{Time: fixedTime, Valid: true},
		UpdatedAt:           sql.NullTime{Time: fixedTime, Valid: true},
	}

	m := jsonField(t, lessonDTO(l))

	if got := string(m["parent_railcontent_id"]); got != "7" {
		t.Errorf("parent_railcontent_id = %s, want 7", got)
	}
	if got := string(m["quality"]); got != `"1080p"` {
		t.Errorf("quality = %s, want \"1080p\"", got)
	}
	if got := string(m["bytes"]); got != "1024" {
		t.Errorf("bytes = %s, want 1024", got)
	}
	if got := string(m["follow_id"]); got != "3" {
		t.Errorf("follow_id = %s, want 3", got)
	}
	if got := string(m["error"]); got != `""` {
		t.Errorf("error = %s, want empty-string scalar", got)
	}
	if got := string(m["downloaded_at"]); got != `"2026-05-30T12:00:00Z"` {
		t.Errorf("downloaded_at = %s, want RFC3339 scalar", got)
	}
}

func TestJobDTO_NullsMarshalAsNull(t *testing.T) {
	j := database.Job{
		ID:            5,
		FollowID:      sql.NullInt64{}, // NULL
		RailcontentID: 42,
		Status:        database.JobQueued,
		Attempts:      0,
		Error:         sql.NullString{}, // NULL
		CreatedAt:     sql.NullTime{},   // NULL
		StartedAt:     sql.NullTime{},   // NULL
		FinishedAt:    sql.NullTime{},   // NULL
	}

	m := jsonField(t, jobDTO(j))

	for _, key := range []string{
		"follow_id", "error", "created_at", "started_at", "finished_at",
	} {
		if got := string(m[key]); got != "null" {
			t.Errorf("%s = %s, want null", key, got)
		}
	}
	if got := string(m["id"]); got != "5" {
		t.Errorf("id = %s, want 5", got)
	}
	if got := string(m["status"]); got != `"queued"` {
		t.Errorf("status = %s, want \"queued\"", got)
	}
}

func TestJobDTO_SetValuesMarshalAsScalar(t *testing.T) {
	j := database.Job{
		ID:            8,
		FollowID:      sql.NullInt64{Int64: 3, Valid: true},
		RailcontentID: 42,
		Status:        database.JobDone,
		Attempts:      2,
		Error:         sql.NullString{String: "boom", Valid: true},
		CreatedAt:     sql.NullTime{Time: fixedTime, Valid: true},
		StartedAt:     sql.NullTime{Time: fixedTime, Valid: true},
		FinishedAt:    sql.NullTime{Time: fixedTime, Valid: true},
	}

	m := jsonField(t, jobDTO(j))

	if got := string(m["follow_id"]); got != "3" {
		t.Errorf("follow_id = %s, want 3", got)
	}
	if got := string(m["error"]); got != `"boom"` {
		t.Errorf("error = %s, want \"boom\"", got)
	}
	if got := string(m["attempts"]); got != "2" {
		t.Errorf("attempts = %s, want 2", got)
	}
	if got := string(m["finished_at"]); got != `"2026-05-30T12:00:00Z"` {
		t.Errorf("finished_at = %s, want RFC3339 scalar", got)
	}
}

func TestSliceMappers(t *testing.T) {
	follows := followDTOs([]database.Follow{{ID: 1, Kind: "node"}, {ID: 2, Kind: "instructor"}})
	if len(follows) != 2 || follows[0].ID != 1 || follows[1].ID != 2 {
		t.Errorf("followDTOs = %+v, want two ordered DTOs", follows)
	}

	lessons := lessonDTOs([]database.Lesson{{RailcontentID: 10}, {RailcontentID: 20}})
	if len(lessons) != 2 || lessons[0].RailcontentID != 10 || lessons[1].RailcontentID != 20 {
		t.Errorf("lessonDTOs = %+v, want two ordered DTOs", lessons)
	}

	jobs := jobDTOs([]database.Job{{ID: 100}, {ID: 200}})
	if len(jobs) != 2 || jobs[0].ID != 100 || jobs[1].ID != 200 {
		t.Errorf("jobDTOs = %+v, want two ordered DTOs", jobs)
	}
}

func TestSliceMappers_EmptyAndNil(t *testing.T) {
	if got := followDTOs(nil); got == nil || len(got) != 0 {
		t.Errorf("followDTOs(nil) = %v, want non-nil empty slice", got)
	}
	if got := lessonDTOs(nil); got == nil || len(got) != 0 {
		t.Errorf("lessonDTOs(nil) = %v, want non-nil empty slice", got)
	}
	if got := jobDTOs(nil); got == nil || len(got) != 0 {
		t.Errorf("jobDTOs(nil) = %v, want non-nil empty slice", got)
	}
}

// TestLessonDTO_HasFiles pins has_files on the wire: always present, true
// exactly when a delete of the lesson would have files to act on (the store's
// own predicate, database.Lesson.HasFiles), whatever the status. A skipped or
// failed lesson that still records files says so; an empty record does not.
func TestLessonDTO_HasFiles(t *testing.T) {
	for _, c := range []struct {
		name string
		l    database.Lesson
		want string
	}{
		{"nothing recorded", database.Lesson{Status: database.StatusPending}, "false"},
		{"own folder, skipped", database.Lesson{Status: database.StatusSkipped, OutputDir: sql.NullString{String: "/dl/C/01 - A", Valid: true}}, "true"},
		{"record only, failed", database.Lesson{Status: database.StatusFailed, LibraryEntries: database.EncodeLibraryEntries([]string{"S/Season 01/a.mp4"})}, "true"},
		{"empty record", database.Lesson{Status: database.StatusSkipped, LibraryEntries: database.EncodeLibraryEntries([]string{})}, "false"},
		{"damaged record", database.Lesson{LibraryEntries: sql.NullString{String: "not json", Valid: true}}, "true"},
	} {
		if got := string(jsonField(t, lessonDTO(c.l))["has_files"]); got != c.want {
			t.Errorf("%s: has_files = %q, want %s", c.name, got, c.want)
		}
		if got := lessonDTO(c.l).HasFiles; got != c.l.HasFiles() {
			t.Errorf("%s: has_files = %v, the store's predicate says %v", c.name, got, c.l.HasFiles())
		}
	}
}
